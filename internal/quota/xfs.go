package quota

import (
	"context"
	"fmt"
	"math"
	"path"
	"strconv"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
)

// xfsMountPoint is where the tool container sees the workspace base dir.
const xfsMountPoint = "/vaultrun-workspaces"

// XFS gives each new session workspace a hard size limit with an XFS project
// quota (WORKSPACE_QUOTA=xfs): writes past MaxBytes fail with "No space
// left on device" in the sandbox, with no overshoot. The base directory must be on
// XFS mounted with prjquota. Setting a quota needs CAP_SYS_ADMIN, so it runs
// as a one-shot privileged container through Docker (see
// internal/xfsquota).
type XFS struct {
	Docker      *dockerpkg.Client
	NextID      func(context.Context) (int64, error) // unique project IDs (DBProjectIDs)
	HostBaseDir string                               // the workspace base directory as the Docker host sees it
	MaxBytes    int64
	Image       string // base image of the tool container (DOCKER_DEFAULT_IMAGE)
}

// Check fails when the workspace base directory has no project quotas. It
// pulls the tool container's image if needed, so later Apply calls find it.
func (x *XFS) Check(ctx context.Context) error {
	if x.Docker.Guarded(ctx) {
		// The guard runs the tool from its own image and binary.
		if err := x.Docker.GuardQuota(ctx, dockerpkg.GuardQuotaRequest{Op: "check"}); err != nil {
			return fmt.Errorf("WORKSPACE_QUOTA=xfs: %w", err)
		}
		return nil
	}
	if ok, err := x.Docker.ImageExists(ctx, x.Image); err != nil || !ok {
		if err := x.Docker.PullImage(ctx, x.Image); err != nil {
			return fmt.Errorf("WORKSPACE_QUOTA=xfs: pull %s: %w", x.Image, err)
		}
	}
	if _, err := x.Docker.RunPrivilegedTool(ctx, x.Image, map[string]string{x.HostBaseDir: xfsMountPoint},
		"xfs-quota", "check", xfsMountPoint); err != nil {
		return fmt.Errorf("WORKSPACE_QUOTA=xfs: %w", err)
	}
	return nil
}

// Apply gives the session's new, empty workspace directory a project of its
// own and limits it to MaxBytes. It must run before anything is written to
// the directory: only files created afterwards inherit the project.
func (x *XFS) Apply(ctx context.Context, sessionID uuid.UUID) error {
	id, err := x.NextID(ctx)
	if err != nil {
		return fmt.Errorf("allocate project id: %w", err)
	}
	if x.Docker.Guarded(ctx) {
		if id <= 0 || id > math.MaxUint32 {
			return fmt.Errorf("project id %d out of range", id)
		}
		if err := x.Docker.GuardQuota(ctx, dockerpkg.GuardQuotaRequest{Op: "set", Name: sessionID.String(), Project: uint32(id), Bytes: x.MaxBytes}); err != nil {
			return fmt.Errorf("set workspace quota: %w", err)
		}
		return nil
	}
	dir := path.Join(xfsMountPoint, sessionID.String())
	if _, err := x.Docker.RunPrivilegedTool(ctx, x.Image, map[string]string{x.HostBaseDir: xfsMountPoint},
		"xfs-quota", "set", dir, strconv.FormatInt(id, 10), strconv.FormatInt(x.MaxBytes, 10)); err != nil {
		return fmt.Errorf("set workspace quota: %w", err)
	}
	return nil
}

// DBProjectIDs allocates project IDs from a database sequence, unique across
// API instances.
func DBProjectIDs(db *sqlx.DB) func(context.Context) (int64, error) {
	return func(ctx context.Context) (int64, error) {
		var id int64
		err := db.GetContext(ctx, &id, `SELECT nextval('workspace_project_id_seq')`)
		return id, err
	}
}
