//go:build windows

package storage

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

type databaseOSLock struct {
	file *os.File
}

var errDatabaseLockBusy = errors.New("database lock is held by another process")

func acquireDatabaseOSLock(path string) (*databaseOSLock, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errDatabaseLockBusy
		}
		return nil, err
	}
	return &databaseOSLock{file: file}, nil
}

func releaseDatabaseOSLock(lock *databaseOSLock) error {
	if lock == nil || lock.file == nil {
		return nil
	}
	return lock.file.Close()
}
