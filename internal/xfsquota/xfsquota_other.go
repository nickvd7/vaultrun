//go:build !linux

package xfsquota

import (
	"errors"
	"fmt"
	"os"
)

var errUnsupported = errors.New("XFS project quotas need Linux")

func Check(string) error                   { return errUnsupported }
func SetProject(string, uint32) error      { return errUnsupported }
func SetLimit(string, uint32, int64) error { return errUnsupported }

func Main([]string) int {
	fmt.Fprintln(os.Stderr, "xfs-quota:", errUnsupported)
	return 1
}
