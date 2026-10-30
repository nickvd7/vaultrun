//go:build linux

// Package xfsquota sets XFS project quotas, the hard per-session limit on a
// workspace (WORKSPACE_QUOTA=xfs).
//
// Every session directory gets a project ID of its own, inherited by
// everything created in it, and a hard block limit for that project: writes
// past it fail with EDQUOT ("Disk quota exceeded") in the sandbox. The
// workspace base directory must be on XFS mounted with prjquota.
//
// Setting a project ID and a limit needs CAP_SYS_ADMIN, which the API does not
// have; it runs this code as a one-shot privileged container (see
// internal/quota). The calls are made directly (ioctl, quotactl_fd) so the
// container needs no xfsprogs and works from any image.
package xfsquota

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fsxattr is struct fsxattr from linux/fs.h.
type fsxattr struct {
	Xflags     uint32
	Extsize    uint32
	Nextents   uint32
	Projid     uint32
	Cowextsize uint32
	Pad        [8]byte
}

const (
	fsIocFsgetxattr    = 0x801c581f // _IOR('X', 31, struct fsxattr)
	fsIocFssetxattr    = 0x401c5820 // _IOW('X', 32, struct fsxattr)
	fsXflagProjinherit = 0x00000200
	prjQuota           = 2
	qXGetQuota         = 0x5803 // XQM_CMD(3)
	qXSetQLim          = 0x5804 // XQM_CMD(4)
	fsDquotVersion     = 1
	fsProjQuota        = 2      // d_flags
	fsDqBSoft          = 1 << 2 // d_fieldmask
	fsDqBHard          = 1 << 3
	basicBlock         = 512 // quota block counts are in 512-byte units
	subcmdShift        = 8
)

// fsDiskQuota is struct fs_disk_quota from linux/dqblk_xfs.h.
type fsDiskQuota struct {
	Version      int8
	Flags        int8
	Fieldmask    uint16
	ID           uint32
	BlkHardlimit uint64
	BlkSoftlimit uint64
	InoHardlimit uint64
	InoSoftlimit uint64
	Bcount       uint64
	Icount       uint64
	Itimer       int32
	Btimer       int32
	Iwarns       uint16
	Bwarns       uint16
	ItimerHi     int8
	BtimerHi     int8
	RtbtimerHi   int8
	Padding2     int8
	RtbHardlimit uint64
	RtbSoftlimit uint64
	Rtbcount     uint64
	Rtbtimer     int32
	Rtbwarns     uint16
	Padding3     int16
	Padding4     [8]byte
}

func qcmd(cmd, typ int) uintptr { return uintptr(cmd<<subcmdShift | typ&0xff) }

func quotactl(path string, cmd int, id uint32, dq *fsDiskQuota) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, _, errno := unix.Syscall6(unix.SYS_QUOTACTL_FD, f.Fd(), qcmd(cmd, prjQuota), uintptr(id), uintptr(unsafe.Pointer(dq)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// SetProject gives dir the project ID id, inherited by everything created in
// it afterwards.
func SetProject(dir string, id uint32) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	var a fsxattr
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), fsIocFsgetxattr, uintptr(unsafe.Pointer(&a))); errno != 0 {
		return fmt.Errorf("get attributes of %s: %w", dir, errno)
	}
	a.Projid = id
	a.Xflags |= fsXflagProjinherit
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), fsIocFssetxattr, uintptr(unsafe.Pointer(&a))); errno != 0 {
		return fmt.Errorf("set project of %s: %w", dir, errno)
	}
	return nil
}

// SetLimit sets the hard (and soft) block limit of project id, in bytes, on
// the filesystem that path is on.
func SetLimit(path string, id uint32, bytes int64) error {
	blocks := uint64((bytes + basicBlock - 1) / basicBlock)
	dq := fsDiskQuota{
		Version:      fsDquotVersion,
		Flags:        fsProjQuota,
		Fieldmask:    fsDqBHard | fsDqBSoft,
		ID:           id,
		BlkHardlimit: blocks,
		BlkSoftlimit: blocks,
	}
	if err := quotactl(path, qXSetQLim, id, &dq); err != nil {
		return fmt.Errorf("set limit of project %d: %w", id, err)
	}
	return nil
}

// Usage returns the bytes used by project id and its hard limit.
func Usage(path string, id uint32) (used, limit int64, err error) {
	var dq fsDiskQuota
	if err := quotactl(path, qXGetQuota, id, &dq); err != nil {
		return 0, 0, err
	}
	return int64(dq.Bcount) * basicBlock, int64(dq.BlkHardlimit) * basicBlock, nil
}

// Check reports whether path is on a filesystem with project quotas enabled.
func Check(path string) error {
	_, _, err := Usage(path, 0)
	switch {
	case err == nil, errors.Is(err, unix.ENOENT):
		return nil
	case errors.Is(err, unix.ESRCH), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOTTY):
		return fmt.Errorf("%s has no project quotas: put it on XFS mounted with prjquota (%w)", path, err)
	default:
		return fmt.Errorf("check project quotas on %s: %w", path, err)
	}
}

// Main runs the privileged helper: "check <path>" or
// "set <dir> <project-id> <bytes>". It returns the exit status.
func Main(args []string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "xfs-quota:", err)
		return 1
	}
	switch {
	case len(args) == 2 && args[0] == "check":
		if err := Check(args[1]); err != nil {
			return fail(err)
		}
		return 0
	case len(args) == 4 && args[0] == "set":
		id, err := strconv.ParseUint(args[2], 10, 32)
		if err != nil || id == 0 {
			return fail(fmt.Errorf("invalid project id %q", args[2]))
		}
		bytes, err := strconv.ParseInt(args[3], 10, 64)
		if err != nil || bytes <= 0 {
			return fail(fmt.Errorf("invalid limit %q", args[3]))
		}
		if err := SetProject(args[1], uint32(id)); err != nil {
			return fail(err)
		}
		if err := SetLimit(args[1], uint32(id), bytes); err != nil {
			return fail(err)
		}
		return 0
	default:
		return fail(errors.New("usage: xfs-quota check <path> | set <dir> <project-id> <bytes>"))
	}
}
