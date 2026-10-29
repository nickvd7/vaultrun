//go:build e2e

package warmpool

import (
	"context"
	"os"
	"testing"
	"time"

	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
)

// TestWarmContainerUsesRuntimeE2E checks that pre-started containers get the
// same OCI runtime (DOCKER_RUNTIME) as on-demand sandboxes.
//
//	VAULTRUN_TEST_RUNTIME=runsc go test -tags e2e ./internal/warmpool/ -v
func TestWarmContainerUsesRuntimeE2E(t *testing.T) {
	runtime := os.Getenv("VAULTRUN_TEST_RUNTIME")
	if runtime == "" {
		t.Skip("VAULTRUN_TEST_RUNTIME not set")
	}
	t.Setenv("DOCKER_RUNTIME", runtime)
	c, err := dockerpkg.New()
	if err != nil {
		t.Skipf("docker client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := c.Inner().Ping(ctx); err != nil {
		t.Skipf("docker daemon unreachable: %v", err)
	}
	const image = "python:3.12-slim"
	if ok, err := c.ImageExists(ctx, image); err != nil || !ok {
		t.Skipf("image %s not available locally", image)
	}

	p := New(c, image, 1, t.TempDir())
	t.Cleanup(p.Stop)
	e, err := p.createOne(ctx)
	if err != nil {
		t.Fatalf("createOne: %v", err)
	}
	// Hand the entry to the pool so Stop removes it.
	p.pool <- e

	info, err := c.Inner().ContainerInspect(ctx, e.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	if info.HostConfig.Runtime != runtime {
		t.Errorf("warm container runtime = %q, want %q", info.HostConfig.Runtime, runtime)
	}
	if !info.State.Running {
		t.Errorf("warm container not running: %s", info.State.Status)
	}
}
