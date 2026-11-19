package template

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// CAS owns its root handle; callers must Close it. Put publishes via an atomic
// no-clobber hard link so concurrent identical writers never replace the object.
type CAS struct{ root *os.Root }

func NewCAS(directory string) (*CAS, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, diagnostic(IOError, "", "cannot create CAS root")
	}
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, diagnostic(IOError, "", "cannot open CAS root")
	}
	return &CAS{root: r}, nil
}

func (c *CAS) Close() error { return c.root.Close() }

// Put returns the root-relative object path. Invalid input is rejected before
// creating directories or files. An existing object is verified, never rewritten.
func (c *CAS) Put(expected string, body []byte) (string, error) {
	if err := VerifyDigest(expected, body); err != nil {
		return "", err
	}
	hexDigest := expected[7:]
	name := filepath.Join("objects", "sha256", hexDigest[:2], hexDigest)
	checkExisting := func() error {
		info, err := c.root.Lstat(name)
		if err != nil || !info.Mode().IsRegular() {
			return diagnostic(CASCorrupt, name, "existing CAS object is not a regular file")
		}
		data, err := c.root.ReadFile(name)
		if err != nil || VerifyDigest(expected, data) != nil {
			return diagnostic(CASCorrupt, name, "existing CAS object is corrupt or unreadable")
		}
		return nil
	}
	if _, err := c.root.Lstat(name); err == nil {
		return name, checkExisting()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", diagnostic(IOError, name, "cannot inspect CAS object")
	}
	dir := filepath.Dir(name)
	if err := c.root.MkdirAll(dir, 0700); err != nil {
		return "", diagnostic(IOError, name, "cannot create CAS shard")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", diagnostic(IOError, name, "cannot create staging identity")
	}
	staging := filepath.Join(dir, ".tmp-"+hex.EncodeToString(random[:]))
	file, err := c.root.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", diagnostic(IOError, name, "cannot create CAS staging file")
	}
	defer c.root.Remove(staging)
	_, writeErr := file.Write(body)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return "", diagnostic(IOError, name, "cannot write CAS staging file")
	}
	if err := c.root.Link(staging, name); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return name, checkExisting()
		}
		return "", diagnostic(IOError, name, "cannot publish CAS object")
	}
	return name, nil
}
