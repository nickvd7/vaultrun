//go:build linux

package xfsquota

import (
	"testing"
	"unsafe"
)

// The structs must match the kernel's byte for byte.
func TestStructLayout(t *testing.T) {
	if n := unsafe.Sizeof(fsxattr{}); n != 28 {
		t.Errorf("fsxattr is %d bytes, want 28", n)
	}
	if n := unsafe.Sizeof(fsDiskQuota{}); n != 112 {
		t.Errorf("fs_disk_quota is %d bytes, want 112", n)
	}
	if off := unsafe.Offsetof(fsDiskQuota{}.BlkHardlimit); off != 8 {
		t.Errorf("d_blk_hardlimit at %d, want 8", off)
	}
	if off := unsafe.Offsetof(fsDiskQuota{}.RtbHardlimit); off != 72 {
		t.Errorf("d_rtb_hardlimit at %d, want 72", off)
	}
	if qcmd(qXSetQLim, prjQuota) != 0x580402 {
		t.Errorf("QCMD(Q_XSETQLIM, PRJQUOTA) = %#x", qcmd(qXSetQLim, prjQuota))
	}
}

// On a filesystem without project quotas (this test's temp dir, normally),
// Check must say so rather than pass.
func TestCheckRefusesWithoutQuotas(t *testing.T) {
	if err := Check(t.TempDir()); err == nil {
		t.Skip("the temp dir has project quotas; nothing to check here")
	}
}
