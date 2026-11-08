//go:build !unix

package fsx

import (
	"errors"
	"os"
)

func openLockFile(string) (*os.File, error) { return nil, errors.ErrUnsupported }

func tryLock(*os.File) (bool, error) { return false, errors.ErrUnsupported }

func unlock(*os.File) error { return errors.ErrUnsupported }

func syncDir(string) error { return nil }
