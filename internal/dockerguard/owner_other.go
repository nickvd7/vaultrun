//go:build !unix

package dockerguard

import (
	"errors"
	"os"
)

func checkBaseOwner(string, os.FileInfo) error {
	return errors.New("the docker guard runs on Linux only")
}
