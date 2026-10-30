package dockerguard

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/nickvd7/vaultrun/internal/docker"
)

// Dirs does the privileged workspace work the API is not allowed to: the
// workspace base directory belongs to root, so the API cannot create, rename
// or remove the session directories in it (and so cannot swap one for a
// symlink to a host path before Docker mounts it). The API owns what is
// inside each session directory.
type Dirs struct {
	Base     string // workspace base directory, as on the Docker host
	UID, GID int    // owner of new session directories (the API's user)
	// ToolImage is the image the XFS quota tool container is created from;
	// ToolBinary (this program, static) is copied into it.
	ToolImage, ToolBinary string

	up *client.Client
}

func (d *Dirs) handleCreate(w http.ResponseWriter, r *http.Request) error {
	var req docker.GuardWorkspaceRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if !WorkspaceNameRe.MatchString(req.Name) {
		return denied("workspace name %q is not allowed", req.Name)
	}
	if req.Mode != 0o700 && req.Mode != 0o777 {
		return denied("workspace mode %o is not allowed", req.Mode)
	}
	path := filepath.Join(d.Base, req.Name)
	if err := os.Mkdir(path, req.Mode); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create workspace: %w", err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return denied("%s exists and is not a directory", req.Name)
	}
	if err := os.Lchown(path, d.UID, d.GID); err != nil {
		return fmt.Errorf("chown workspace: %w", err)
	}
	if err := os.Chmod(path, req.Mode); err != nil {
		return fmt.Errorf("chmod workspace: %w", err)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": path})
	return nil
}

func (d *Dirs) handleRemove(w http.ResponseWriter, name string) error {
	if !WorkspaceNameRe.MatchString(name) {
		return denied("workspace name %q is not allowed", name)
	}
	// RemoveAll does not follow symlinks the sandbox left in the directory.
	if err := os.RemoveAll(filepath.Join(d.Base, name)); err != nil {
		return fmt.Errorf("remove workspace: %w", err)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

const toolMount = "/vaultrun-workspaces"

func (d *Dirs) handleQuota(w http.ResponseWriter, r *http.Request) error {
	var req docker.GuardQuotaRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	var args []string
	switch req.Op {
	case "check":
		args = []string{"check", toolMount}
	case "set":
		if !WorkspaceNameRe.MatchString(req.Name) || req.Project == 0 || req.Bytes <= 0 {
			return denied("invalid quota request")
		}
		args = []string{"set", toolMount + "/" + req.Name, strconv.FormatUint(uint64(req.Project), 10), strconv.FormatInt(req.Bytes, 10)}
	default:
		return denied("quota op %q is not allowed", req.Op)
	}
	out, err := d.runQuotaTool(r.Context(), args)
	if err != nil {
		return statusErr{http.StatusUnprocessableEntity, err}
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

// runQuotaTool runs "vr-exec xfs-quota args..." as root with CAP_SYS_ADMIN in
// a one-shot container on the host kernel, with only the workspace base
// directory mounted. The binary is copied into the container, not taken from
// a volume the API can write to.
func (d *Dirs) runQuotaTool(ctx context.Context, args []string) (string, error) {
	if d.ToolImage == "" || d.ToolBinary == "" {
		return "", errors.New("the quota tool is not configured (VAULTRUN_TOOL_IMAGE)")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := d.up.ContainerCreate(ctx,
		&container.Config{
			Image:      d.ToolImage,
			User:       "0:0",
			Entrypoint: []string{"/vr-exec"},
			Cmd:        append([]string{"xfs-quota"}, args...),
			Env:        []string{},
		},
		&container.HostConfig{
			NetworkMode: "none",
			Mounts:      []mount.Mount{{Type: mount.TypeBind, Source: d.Base, Target: toolMount}},
			CapDrop:     []string{"ALL"},
			CapAdd:      []string{"SYS_ADMIN", "FOWNER"},
			// quotactl_fd is not in every daemon's default seccomp profile.
			SecurityOpt: []string{"no-new-privileges", "seccomp=unconfined"},
			// The calls must reach the host kernel, not gVisor's.
			Runtime: "runc",
		}, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("create quota tool container: %w", err)
	}
	defer func() {
		_ = d.up.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
	}()
	bin, err := os.ReadFile(d.ToolBinary)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "vr-exec", Mode: 0o555, Size: int64(len(bin)), ModTime: time.Now(), Typeflag: tar.TypeReg})
	_, _ = tw.Write(bin)
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := d.up.CopyToContainer(ctx, resp.ID, "/", &buf, container.CopyToContainerOptions{}); err != nil {
		return "", fmt.Errorf("copy quota tool: %w", err)
	}
	if err := d.up.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start quota tool: %w", err)
	}
	statusCh, errCh := d.up.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	var code int64
	select {
	case err := <-errCh:
		return "", fmt.Errorf("wait for quota tool: %w", err)
	case st := <-statusCh:
		code = st.StatusCode
	}
	var out bytes.Buffer
	if logs, err := d.up.ContainerLogs(ctx, resp.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true}); err == nil {
		_, _ = stdcopy.StdCopy(&out, &out, io.LimitReader(logs, 64<<10))
		logs.Close()
	}
	text := strings.TrimSpace(out.String())
	if code != 0 {
		return text, fmt.Errorf("tool exited with status %d: %s", code, text)
	}
	return text, nil
}
