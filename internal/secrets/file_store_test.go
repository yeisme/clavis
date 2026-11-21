package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKeysFile(t *testing.T, dir, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, "ai-keys.toml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFileStoreRoundTripsAKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ai-keys.toml")
	fs := NewFileStore(path)

	if err := fs.SaveKey("acct.one", "secret-value"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := fs.ReadKey("acct.one")
	if err != nil || got != "secret-value" {
		t.Fatalf("read = %q, %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode = %04o, want 0600", perm)
	}
	if src, _ := fs.ResolveAPIKeySource("acct.one"); src != KeySourceFile {
		t.Fatalf("source = %q, want file", src)
	}
}

// A file other users can read must be refused rather than silently used: the whole point of
// moving off the keychain is that the key nobody else can read.
func TestFileStoreRejectsGroupOrWorldReadableFile(t *testing.T) {
	dir := t.TempDir()
	path := writeKeysFile(t, dir, "[keys]\nacct.one = \"secret\"\n", 0o644)
	fs := NewFileStore(path)
	if _, err := fs.ReadKey("acct.one"); err == nil {
		t.Fatal("a 0644 keys file must be rejected")
	} else if !strings.Contains(err.Error(), "0600") {
		t.Fatalf("error should name the expected mode: %v", err)
	}
}

func TestFileStoreMissingFileIsNotAnError(t *testing.T) {
	fs := NewFileStore(filepath.Join(t.TempDir(), "absent.toml"))
	if _, err := fs.ReadKey("acct.one"); err != ErrSecretNotFound {
		t.Fatalf("err = %v, want ErrSecretNotFound", err)
	}
	if err := fs.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil for an absent file", err)
	}
}

// After the one-off migration the AI path must never reach the keychain again: that read is
// what raised a SecurityAgent dialog on every keystroke once the app had been rebuilt.
func TestLayeredStoreNeverTouchesKeychainWhenMigrationIsOff(t *testing.T) {
	t.Setenv(DefaultEnvVar, "")
	kc := &countingStore{}
	fs := NewFileStore(filepath.Join(t.TempDir(), "ai-keys.toml"))
	layered := &LayeredStore{File: fs, Keychain: kc} // Migrate defaults to false

	if _, err := layered.ReadKey("acct.one"); err != ErrSecretNotFound {
		t.Fatalf("err = %v, want ErrSecretNotFound", err)
	}
	if _, err := layered.ResolveAPIKey("acct.one"); err != ErrSecretNotFound {
		t.Fatalf("resolve err = %v, want ErrSecretNotFound", err)
	}
	if kc.calls != 0 {
		t.Fatalf("keychain was consulted %d times; it must never be touched after migration", kc.calls)
	}
}

func TestLayeredStoreResolvePriorityAndEnvIsolation(t *testing.T) {
	t.Setenv(DefaultEnvVar, "env-value")

	t.Run("environment source", func(t *testing.T) {
		kc := &countingStore{}
		layered := &LayeredStore{File: NewFileStore(filepath.Join(t.TempDir(), "ai-keys.toml")), Keychain: kc}
		got, err := layered.ResolveAPIKey("acct.one")
		if err != nil || got != "env-value" {
			t.Fatalf("resolve = %q, %v; want env-value", got, err)
		}
		source, err := layered.ResolveAPIKeySource("acct.one")
		if err != nil || source != KeySourceEnv {
			t.Fatalf("source = %q, %v; want env", source, err)
		}
		if kc.calls != 0 {
			t.Fatalf("keychain was consulted %d times for env source", kc.calls)
		}
	})

	t.Run("file takes precedence over environment", func(t *testing.T) {
		kc := &countingStore{}
		fs := NewFileStore(filepath.Join(t.TempDir(), "ai-keys.toml"))
		if err := fs.SaveKey("acct.one", "file-value"); err != nil {
			t.Fatal(err)
		}
		layered := &LayeredStore{File: fs, Keychain: kc}
		got, err := layered.ResolveAPIKey("acct.one")
		if err != nil || got != "file-value" {
			t.Fatalf("resolve = %q, %v; want file-value", got, err)
		}
		source, err := layered.ResolveAPIKeySource("acct.one")
		if err != nil || source != KeySourceFile {
			t.Fatalf("source = %q, %v; want file", source, err)
		}
		if kc.calls != 0 {
			t.Fatalf("keychain was consulted %d times for file source", kc.calls)
		}
	})
}

// Writes always land in the file, never the keychain, so a rebuild can never orphan a newly
// saved key.
func TestLayeredStoreWritesOnlyToFile(t *testing.T) {
	kc := &countingStore{}
	fs := NewFileStore(filepath.Join(t.TempDir(), "ai-keys.toml"))
	layered := &LayeredStore{File: fs, Keychain: kc}

	if err := layered.SaveKey("acct.one", "v"); err != nil {
		t.Fatal(err)
	}
	if kc.saveCalls != 0 {
		t.Fatalf("keychain save called %d times", kc.saveCalls)
	}
	if got, err := fs.ReadKey("acct.one"); err != nil || got != "v" {
		t.Fatalf("file read = %q, %v", got, err)
	}
}

func TestLayeredStoreMigrationCopiesKeychainOnce(t *testing.T) {
	kc := &countingStore{value: "from-keychain"}
	fs := NewFileStore(filepath.Join(t.TempDir(), "ai-keys.toml"))
	layered := &LayeredStore{File: fs, Keychain: kc}

	moved, err := layered.MigrateFromKeychain("acct.one")
	if err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	if got, _ := fs.ReadKey("acct.one"); got != "from-keychain" {
		t.Fatalf("file has %q", got)
	}
	moved, err = layered.MigrateFromKeychain("acct.one")
	if err != nil || moved {
		t.Fatalf("second migration should be a no-op: moved=%v err=%v", moved, err)
	}
}

type countingStore struct {
	calls, saveCalls int
	value            string
}

func (c *countingStore) ReadKey(string) (string, error) {
	c.calls++
	if c.value == "" {
		return "", ErrSecretNotFound
	}
	return c.value, nil
}
func (c *countingStore) SaveKey(string, string) error { c.saveCalls++; return nil }
func (c *countingStore) DeleteKey(string) error       { return nil }
func (c *countingStore) ResolveAPIKey(string) (string, error) {
	return c.ReadKey("")
}
func (c *countingStore) ResolveAPIKeySource(string) (APIKeySource, error) {
	return KeySourceKeychain, nil
}
func (c *countingStore) ResolveAPIKeyWithEnv(string, string) (string, error) {
	return c.ReadKey("")
}
func (c *countingStore) ResolveAPIKeySourceWithEnv(string, string) (APIKeySource, error) {
	return KeySourceKeychain, nil
}

// A dotted account name must survive a save/load round trip. TOML parses an unquoted
// clavis.ai.magpie as nested tables, so the account silently became "clavis" and every AI
// request looked unconfigured — which is what pushed users back into keychain prompts.
func TestFileStoreRoundTripsDottedAccountName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ai-keys.toml")
	fs := NewFileStore(path)
	if err := fs.SaveKey("clavis.ai.magpie", "v1"); err != nil {
		t.Fatal(err)
	}
	fresh := NewFileStore(path) // force a re-read from disk
	got, err := fresh.ReadKey("clavis.ai.magpie")
	if err != nil || got != "v1" {
		t.Fatalf("dotted account read = %q, %v", got, err)
	}
	if _, err := fresh.ReadKey("clavis"); err == nil {
		t.Fatal("the truncated name must not resolve")
	}
}

// Older files may hold the dotted name unquoted, which parses as nested tables; reading must
// flatten that back to the dotted name rather than treating it as a different account.
func TestFileStoreReadsLegacyNestedDottedKeys(t *testing.T) {
	dir := t.TempDir()
	path := writeKeysFile(t, dir, "[keys]\nclavis.ai.magpie = \"v1\"\n", 0o600)
	fs := NewFileStore(path)
	got, err := fs.ReadKey("clavis.ai.magpie")
	if err != nil || got != "v1" {
		t.Fatalf("legacy nested read = %q, %v", got, err)
	}
}
