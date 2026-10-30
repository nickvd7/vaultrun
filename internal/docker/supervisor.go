package docker

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"

	"github.com/nickvd7/vaultrun/internal/supervise"
)

// supervisorVolume holds the run supervisor (see internal/supervise); it is
// mounted read-only into every sandbox, so the sandbox cannot replace it.
const supervisorVolume = "vaultrun-tools"

// labelSupervisor marks sandboxes that have the supervisor mounted. Sandboxes
// created before it existed run commands directly, as they always did.
const labelSupervisor = "vaultrun.supervisor"

type supervisorState struct {
	mu     sync.Mutex
	binary string // static binary to install as vr-exec; "" = disabled
	ready  bool
	failAt time.Time
}

// SetSupervisorBinary sets the static binary installed into sandboxes as the
// run supervisor (normally the API binary itself, which starts the supervisor
// when invoked as vr-exec). Without it, commands run directly and a timed-out
// command keeps running in the sandbox.
func (c *Client) SetSupervisorBinary(path string) {
	c.supervisor.mu.Lock()
	defer c.supervisor.mu.Unlock()
	c.supervisor.binary = path
	c.supervisor.ready = false
}

// supervisorMount installs the supervisor into its volume once (using image,
// which the caller is about to start a sandbox from, so it is present) and
// returns the mount for a new sandbox. ok is false when no supervisor is
// configured or the installation failed; the sandbox then runs without it.
func (c *Client) supervisorMount(ctx context.Context, image string) (m mount.Mount, ok bool) {
	s := &c.supervisor
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.binary == "" {
		return mount.Mount{}, false
	}
	if !s.ready {
		// Do not retry a failing installation on every sandbox.
		if time.Since(s.failAt) < time.Minute {
			return mount.Mount{}, false
		}
		if err := c.installSupervisor(ctx, image, s.binary); err != nil {
			s.failAt = time.Now()
			slog.Error("sandbox: installing the run supervisor failed; timeouts will not stop commands", "err", err)
			return mount.Mount{}, false
		}
		s.ready = true
	}
	return mount.Mount{
		Type:          mount.TypeVolume,
		Source:        supervisorVolume,
		Target:        supervise.Dir,
		ReadOnly:      true,
		VolumeOptions: &mount.VolumeOptions{NoCopy: true},
	}, true
}

// SupervisorMount is supervisorMount for callers outside this package that
// create sandboxes themselves (the warm pool); they also set the label
// returned by SupervisorLabel.
func (c *Client) SupervisorMount(ctx context.Context, image string) (mount.Mount, bool) {
	return c.supervisorMount(ctx, image)
}

// SupervisorLabel is the container label that marks a supervised sandbox.
func SupervisorLabel() (key, value string) { return labelSupervisor, "1" }

// installSupervisor copies binary into the supervisor volume as vr-exec. The
// volume is written through a container that is created but never started.
func (c *Client) installSupervisor(ctx context.Context, image, binary string) error {
	f, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	if _, err := c.inner.VolumeCreate(ctx, volume.CreateOptions{
		Name:   supervisorVolume,
		Labels: map[string]string{"vaultrun.managed": "true"},
	}); err != nil {
		return fmt.Errorf("create volume: %w", err)
	}
	resp, err := c.inner.ContainerCreate(ctx,
		&container.Config{Image: image, Cmd: []string{"true"}, Labels: map[string]string{"vaultrun.managed": "true"}},
		&container.HostConfig{
			NetworkMode: "none",
			Mounts:      []mount.Mount{{Type: mount.TypeVolume, Source: supervisorVolume, Target: "/vaultrun-tools"}},
		}, nil, nil, "")
	if err != nil {
		return fmt.Errorf("create helper container: %w", err)
	}
	defer func() {
		_ = c.inner.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
	}()

	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := tw.WriteHeader(&tar.Header{
			Name: supervise.Name, Mode: 0o555, Size: info.Size(),
			Uid: 0, Gid: 0, ModTime: time.Now(), Typeflag: tar.TypeReg,
		})
		if err == nil {
			_, err = io.Copy(tw, f)
		}
		if err == nil {
			err = tw.Close()
		}
		pw.CloseWithError(err)
	}()
	if err := c.inner.CopyToContainer(ctx, resp.ID, "/vaultrun-tools", pr, types.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copy supervisor: %w", err)
	}
	slog.Info("sandbox: run supervisor installed", "volume", supervisorVolume, "bytes", info.Size())
	return nil
}

// RunPrivilegedTool runs the API binary from the supervisor volume as root
// with CAP_SYS_ADMIN in a one-shot container (no network, image as the base
// filesystem) and returns its combined output. binds maps host paths to
// container paths. It exists for the XFS quota helper, which needs
// quotactl; the API itself has no such capability.
func (c *Client) RunPrivilegedTool(ctx context.Context, image string, binds map[string]string, args ...string) (string, error) {
	sup, ok := c.supervisorMount(ctx, image)
	if !ok {
		return "", fmt.Errorf("the VaultRun tools volume is not available")
	}
	mounts := []mount.Mount{sup}
	for host, target := range binds {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: host, Target: target})
	}
	resp, err := c.inner.ContainerCreate(ctx,
		&container.Config{
			Image:      image,
			User:       "0:0",
			Entrypoint: []string{supervise.Path},
			Cmd:        args,
			Labels:     map[string]string{"vaultrun.managed": "true"},
		},
		&container.HostConfig{
			NetworkMode: "none",
			Mounts:      mounts,
			CapDrop:     []string{"ALL"},
			CapAdd:      []string{"SYS_ADMIN", "FOWNER"},
			// quotactl_fd is not in every daemon's default seccomp profile.
			SecurityOpt: []string{"no-new-privileges", "seccomp=unconfined"},
			// The calls must reach the host kernel, not gVisor's.
			Runtime: "runc",
		}, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("create tool container: %w", err)
	}
	defer func() {
		_ = c.inner.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
	}()
	if err := c.inner.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start tool container: %w", err)
	}
	statusCh, errCh := c.inner.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	var code int64
	select {
	case err := <-errCh:
		return "", fmt.Errorf("wait for tool container: %w", err)
	case st := <-statusCh:
		code = st.StatusCode
	}
	logs, err := c.inner.ContainerLogs(ctx, resp.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	var out strings.Builder
	if err == nil {
		_ = demuxDockerStream(logs, &out, &out)
		logs.Close()
	}
	if code != 0 {
		return out.String(), fmt.Errorf("tool exited with status %d: %s", code, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}
