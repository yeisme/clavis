package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func configFileBytes(t *testing.T, dir string) map[ConfigFileKind][]byte {
	t.Helper()
	out := make(map[ConfigFileKind][]byte)
	for _, kind := range []ConfigFileKind{FileMain, FileLeader, FileSnippets, FileGlossary, FileIMERules, FilePrompts, FileComponents, FileWorkflows} {
		data, err := os.ReadFile(filepath.Join(dir, string(kind)))
		if err != nil {
			t.Fatalf("read %s: %v", kind, err)
		}
		out[kind] = data
	}
	return out
}

func TestSaveAllFailureRollsBackCommittedFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	before := configFileBytes(t, dir)
	old := store.Current()
	candidate := old
	candidate.Main.Appearance.CapsuleWidth++

	var calls int
	store.writeConfigFile = func(dir string, kind ConfigFileKind, content string) error {
		calls++
		if kind == FileWorkflows {
			return errors.New("injected SaveAll write failure")
		}
		return atomicWriteConfigFile(dir, kind, content)
	}
	if err := store.SaveAll(candidate); err == nil {
		t.Fatal("SaveAll unexpectedly succeeded")
	}
	if calls != 8 {
		t.Fatalf("SaveAll attempted %d files, want 8 including failed final write", calls)
	}
	if got := store.Current(); !reflect.DeepEqual(got, old) {
		t.Fatalf("failed SaveAll changed Current: got %#v want %#v", got, old)
	}
	after := configFileBytes(t, dir)
	for kind, want := range before {
		if !bytes.Equal(after[kind], want) {
			t.Errorf("failed SaveAll changed %s", kind)
		}
	}
}

func TestSaveAllSerializesReloadAfterCommit(t *testing.T) {
	dir := t.TempDir()
	store := NewConfigStore(dir)
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	candidate := store.Current()
	candidate.Main.Appearance.CapsuleWidth++

	firstWrite := make(chan struct{})
	releaseWrite := make(chan struct{})
	var once sync.Once
	store.writeConfigFile = func(dir string, kind ConfigFileKind, content string) error {
		err := atomicWriteConfigFile(dir, kind, content)
		if kind == FileMain {
			once.Do(func() {
				close(firstWrite)
				<-releaseWrite
			})
		}
		return err
	}

	saveDone := make(chan error, 1)
	go func() { saveDone <- store.SaveAll(candidate) }()
	select {
	case <-firstWrite:
	case <-time.After(time.Second):
		t.Fatal("SaveAll did not start")
	}

	reloadDone := make(chan bool, 1)
	go func() { reloadDone <- store.Reload() }()
	select {
	case <-reloadDone:
		t.Fatal("Reload completed while SaveAll was still committing")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseWrite)

	select {
	case err := <-saveDone:
		if err != nil {
			t.Fatalf("SaveAll: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SaveAll did not finish")
	}
	select {
	case ok := <-reloadDone:
		if !ok {
			t.Fatalf("Reload after SaveAll failed: %v", store.LastErrors())
		}
	case <-time.After(time.Second):
		t.Fatal("Reload did not finish")
	}
	if got := store.Current(); !reflect.DeepEqual(got, candidate) {
		t.Fatalf("Reload installed stale config: got %#v want %#v", got, candidate)
	}
}
