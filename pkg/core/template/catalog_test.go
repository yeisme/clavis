package template

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureRoot = "testdata/synthetic"
const fixtureDigest = "sha256:d39ed3783b52786faed23719b43f7efdca0dbd5d4391ed39cff437aaa41c093e"

func fixture(t *testing.T) Catalog {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureRoot, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	c, diagnostics := ParseCatalog(data)
	if len(diagnostics) != 0 || c == nil {
		t.Fatalf("fixture rejected: %v", diagnostics)
	}
	return *c
}

func signedJSON(t *testing.T, c Catalog) []byte {
	t.Helper()
	for i := range c.Solutions {
		var err error
		c.Solutions[i].Digest, err = CanonicalSolutionDigest(c.Solutions[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	c.Digest, err = CanonicalCatalogDigest(c)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertCode(t *testing.T, diagnostics []Diagnostic, code Code) {
	t.Helper()
	for _, d := range diagnostics {
		if d.Code == code && d.Path != "" && d.Message != "" {
			return
		}
	}
	t.Fatalf("missing diagnostic %s: %v", code, diagnostics)
}

func copyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureRoot)); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSyntheticCanonicalDigests(t *testing.T) {
	c := fixture(t)
	if len(c.Solutions) != 3 || len(c.Discovery) != 3 {
		t.Fatal("synthetic fixture must contain three solutions and discovery records")
	}
	actual, err := CanonicalCatalogDigest(c)
	if err != nil || actual != fixtureDigest || actual != c.Digest {
		t.Fatalf("canonical catalog: %s, %v", actual, err)
	}
	expected := []string{
		"sha256:024159c20c4a0d780d4eb79baa98052c4cdb6ca9b65ea2820746a2d4816555b7",
		"sha256:7463b8e9364afbf66997906ab555340e6a0be5a39d7bde41f948a98186895b5f",
		"sha256:802491632f6195dd1420111c39a4245942cb2709c19f4d7833b7629e4f7beabc",
	}
	for i, s := range c.Solutions {
		actual, err := CanonicalSolutionDigest(s)
		if err != nil || actual != expected[i] || actual != s.Digest {
			t.Fatalf("canonical solution %d: %s, %v", i, actual, err)
		}
		body, err := os.ReadFile(filepath.Join(fixtureRoot, s.Templates[0].Path))
		if err != nil || VerifyDigest(s.Templates[0].Digest, body) != nil {
			t.Fatalf("template %d failed verification", i)
		}
	}
	if loaded, diagnostics := LoadCatalog(fixtureRoot); loaded == nil || len(diagnostics) != 0 {
		t.Fatalf("complete distribution: %v", diagnostics)
	}
}

func TestCanonicalRulesAndNoMutation(t *testing.T) {
	c := fixture(t)
	originalFirst := c.Solutions[0].Key()
	c.GeneratedAt = time.Now()
	c.Discovery = nil
	c.Digest = "ignored"
	c.Solutions[0], c.Solutions[2] = c.Solutions[2], c.Solutions[0]
	firstBefore := c.Solutions[0].Key()
	actual, err := CanonicalCatalogDigest(c)
	if err != nil || actual != fixtureDigest {
		t.Fatalf("transient fields and solution order must not matter: %s %v", actual, err)
	}
	if c.Solutions[0].Key() != firstBefore || firstBefore == originalFirst {
		t.Fatal("digest changed caller-owned solution order")
	}
	s := c.Solutions[0]
	want := s.Digest
	s.Digest = "ignored"
	s.RepositoryID = "runtime-only"
	actual, err = CanonicalSolutionDigest(s)
	if err != nil || actual != want {
		t.Fatalf("runtime repository ID must be excluded: %s %v", actual, err)
	}
}

func TestMarshalFieldOrderOmissionsAndHTMLEscaping(t *testing.T) {
	s := Solution{PackageID: "demo", ID: "x", Version: "1", Category: "text", Rights: "MIT", Maturity: "test", Locales: map[string]Locale{"en": {Title: "<>&"}}, RepositoryID: "runtime"}
	actual, err := CanonicalSolutionDigest(s)
	const canonical = `{"package_id":"demo","id":"x","version":"1","digest":"","category":"text","rights":"MIT","maturity":"test","locales":{"en":{"title":"\u003c\u003e\u0026","summary":"","usage":""}}}`
	if err != nil || actual != Digest([]byte(canonical)) {
		t.Fatalf("field order / omitempty / HTML escaping differs: %s %v", actual, err)
	}
}

func TestDigestFormat(t *testing.T) {
	for _, value := range []string{"", "sha256:abc", "SHA256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("A", 64), "sha256:" + strings.Repeat("g", 64), "sha256:" + strings.Repeat("0", 65)} {
		if err := ValidateDigest(value); err == nil {
			t.Fatalf("accepted invalid digest %q", value)
		}
	}
	if err := ValidateDigest("sha256:" + strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
}

func TestTamperingEveryLayerFailsClosed(t *testing.T) {
	t.Run("catalog one byte", func(t *testing.T) {
		data, _ := os.ReadFile(filepath.Join(fixtureRoot, "catalog.json"))
		data = bytes.Replace(data, []byte("Synthetic catalog"), []byte("synthetic catalog"), 1)
		c, diagnostics := ParseCatalog(data)
		if c != nil {
			t.Fatal("tampered catalog is usable")
		}
		assertCode(t, diagnostics, DigestMismatch)
	})
	t.Run("solution with valid outer digest", func(t *testing.T) {
		c := fixture(t)
		c.Solutions[0].Rights = "changed"
		c.Digest, _ = CanonicalCatalogDigest(c)
		data, _ := json.Marshal(c)
		loaded, diagnostics := ParseCatalog(data)
		if loaded != nil {
			t.Fatal("tampered solution is usable")
		}
		assertCode(t, diagnostics, DigestMismatch)
	})
	t.Run("template one byte", func(t *testing.T) {
		dir := copyFixture(t)
		c := fixture(t)
		name := filepath.Join(dir, c.Solutions[0].Templates[0].Path)
		body, _ := os.ReadFile(name)
		body[len(body)-1] ^= 1
		if err := os.WriteFile(name, body, 0600); err != nil {
			t.Fatal(err)
		}
		loaded, diagnostics := LoadCatalog(dir)
		if loaded != nil {
			t.Fatal("tampered body is usable")
		}
		assertCode(t, diagnostics, DigestMismatch)
	})
}

func TestMissingTemplatesAndSchema(t *testing.T) {
	c := fixture(t)
	c.Solutions[0].Templates = nil
	loaded, diagnostics := ParseCatalog(signedJSON(t, c))
	if loaded != nil {
		t.Fatal("missing templates must reject the item")
	}
	assertCode(t, diagnostics, MissingTemplates)
	c = fixture(t)
	c.SchemaVersion = "promptrepo.catalog.v999"
	loaded, diagnostics = ParseCatalog(signedJSON(t, c))
	if loaded != nil {
		t.Fatal("unknown schema accepted")
	}
	assertCode(t, diagnostics, UnsupportedSchema)
	loaded, diagnostics = ParseCatalog([]byte(`{"schema_version":`))
	if loaded != nil {
		t.Fatal("invalid JSON accepted")
	}
	assertCode(t, diagnostics, InvalidJSON)
}

func TestUnsafeReferences(t *testing.T) {
	for _, unsafe := range []string{"../body.md", "solutions/x/../body.md", "/tmp/body.md", `solutions\..\body.md`, "C:/body.md", "solutions//body.md"} {
		t.Run(unsafe, func(t *testing.T) {
			c := fixture(t)
			c.Solutions[0].Templates[0].Path = unsafe
			loaded, diagnostics := ParseCatalog(signedJSON(t, c))
			if loaded != nil {
				t.Fatal("unsafe path accepted")
			}
			assertCode(t, diagnostics, UnsafePath)
		})
	}
}

func TestDanglingReferencesAndSymlinkEscape(t *testing.T) {
	for _, kind := range []string{"body", "sidecar", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := copyFixture(t)
			tmpl := fixture(t).Solutions[0].Templates[0]
			name := tmpl.Path
			if kind == "sidecar" {
				var err error
				name, err = ContractPath(tmpl)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				outside := filepath.Join(t.TempDir(), "outside.md")
				body, _ := os.ReadFile(filepath.Join(fixtureRoot, tmpl.Path))
				if err := os.WriteFile(outside, body, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			}
			loaded, diagnostics := LoadCatalog(dir)
			if loaded != nil {
				t.Fatal("dangling reference accepted")
			}
			assertCode(t, diagnostics, DanglingReference)
		})
	}
}

func TestCASIdempotentAndBadDigestRejected(t *testing.T) {
	dir := t.TempDir()
	cas, err := NewCAS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cas.Close()
	body := []byte("synthetic CAS object\n")
	if _, err := cas.Put(Digest([]byte("different")), body); err == nil {
		t.Fatal("bad digest accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "objects")); !os.IsNotExist(err) {
		t.Fatal("bad input wrote CAS directories")
	}
	name, err := cas.Put(Digest(body), body)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("objects", "sha256", Digest(body)[7:9], Digest(body)[7:])
	if name != want {
		t.Fatalf("wrong content path: %s", name)
	}
	oldTime := time.Unix(1234567890, 0)
	if err := os.Chtimes(filepath.Join(dir, name), oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(filepath.Join(dir, name))
	if again, err := cas.Put(Digest(body), body); err != nil || again != name {
		t.Fatalf("second put: %s, %v", again, err)
	}
	after, _ := os.Stat(filepath.Join(dir, name))
	if !before.ModTime().Equal(after.ModTime()) || !os.SameFile(before, after) {
		t.Fatal("idempotent write changed mtime or inode")
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || !bytes.Equal(data, body) {
		t.Fatal("CAS content differs")
	}
}

func TestCASCorruptionAndConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	cas, err := NewCAS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cas.Close()
	body := []byte("synthetic concurrent object")
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cas.Put(Digest(body), body); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	name, err := cas.Put(Digest(body), body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = cas.Put(Digest(body), body)
	if d, ok := err.(Diagnostic); !ok || d.Code != CASCorrupt {
		t.Fatalf("corrupt CAS must reject rather than overwrite: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, name))
	if string(data) != "corrupt" {
		t.Fatal("corrupt CAS was silently repaired")
	}
}
