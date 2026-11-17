//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package storage

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type databaseOSLock struct {
	file *os.File
	path string
}

var errDatabaseLockBusy = errors.New("database lock is held by another process")

func databaseLockRenameMayFailWithOpenHandle() bool { return false }

func acquireDatabaseOSLock(path string) (*databaseOSLock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errDatabaseLockBusy
		}
		return nil, err
	}
	return &databaseOSLock{file: file, path: path}, nil
}

func releaseDatabaseOSLock(lock *databaseOSLock) error {
	if lock == nil || lock.file == nil {
		return nil
	}
	return lock.file.Close()
}
