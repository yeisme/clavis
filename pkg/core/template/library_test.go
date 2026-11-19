package template

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func libraryFixture(t *testing.T) string {
	t.Helper()
	dir := copyFixture(t)
	c := fixture(t)
	for _, s := range c.Solutions {
		for _, v := range s.Templates {
			name, _ := ContractPath(v)
			b, _ := json.Marshal(map[string]string{"schema_version": ContractSchema, "template_digest": v.Digest, "solution_id": s.ID, "digest": Digest([]byte("declared canonical, not raw bytes"))})
			if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}
func newLibrary(t *testing.T) (*Library, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := OpenLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, dir
}
func noDiagnostics(t *testing.T, ds []Diagnostic) {
	t.Helper()
	if len(ds) > 0 {
		t.Fatalf("unexpected diagnostics: %v", ds)
	}
}
func installSummary(t *testing.T, l *Library, dir string, kind SourceKind) {
	t.Helper()
	noDiagnostics(t, l.Refresh(dir))
	noDiagnostics(t, l.Import(dir, "demo/writing", "demo-summary", "main", "en", kind))
}
func writeCatalog(t *testing.T, dir string, c Catalog) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "catalog.json"), signedJSON(t, c), 0600); err != nil {
		t.Fatal(err)
	}
}
func onlyItem(t *testing.T, l *Library) Item {
	t.Helper()
	rows, ds := l.View(nil)
	noDiagnostics(t, ds)
	if len(rows) != 1 {
		t.Fatalf("rows: %d", len(rows))
	}
	return rows[0]
}

func TestLibraryImportOverlayAndFork(t *testing.T) {
	dir := libraryFixture(t)
	l, root := newLibrary(t)
	installSummary(t, l, dir, SourcePromptrepo)
	item := onlyItem(t, l)
	if item.Source != Imported || item.SourceKind != SourcePromptrepo || item.Provenance == nil || item.ID != "demo-summary.main.en" || item.Body == "" || item.Maturity == "" {
		t.Fatal("missing imported view")
	}
	rec := item.Provenance
	if rec.ContractDigest == rec.DeclaredContractDigest || rec.ContractDigest == "" {
		t.Fatal("raw and canonical contract digests conflated")
	}
	raw, err := l.readObject(rec.ContractDigest)
	if err != nil || Digest(raw) != rec.ContractDigest {
		t.Fatal("sidecar not stored opaque in CAS")
	}
	noDiagnostics(t, l.SaveOverlay("demo-summary", "main", "en", []byte(`trigger = ""
title = "My title"
category = ""
app_bundle_ids = ["com.example.Editor"]
enabled = false
[slot.subject]
default = ""
`)))
	over := onlyItem(t, l)
	if over.Enabled || over.Trigger != "" || over.Category != "" || over.Title != "My title" || len(over.AppBundleIDs) != 1 || *over.SlotDefaults["subject"].Default != "" || over.Body != item.Body {
		t.Fatal("overlay did not preserve explicit empty/false or readonly body")
	}
	noDiagnostics(t, l.SaveOverlay("demo-summary", "main", "en", []byte("app_bundle_ids = []\nenabled = true\n")))
	if got := onlyItem(t, l); !got.Enabled || len(got.AppBundleIDs) != 0 {
		t.Fatal("empty list not applied")
	}
	fork := Fork(over)
	if fork.ID != "local:demo-summary.main.en" || fork.Provenance != nil || fork.Source != Local || fork.SourceKind != SourceLocal || fork.Maturity != "" || fork.UpdateStatus != "no_updates" || fork.Body != over.Body {
		t.Fatal("fork retains provenance or loses content")
	}
	fork.AppBundleIDs[0] = "changed"
	*fork.SlotDefaults["subject"].Default = "changed"
	if over.AppBundleIDs[0] == "changed" || *over.SlotDefaults["subject"].Default != "" {
		t.Fatal("fork aliases source")
	}
	b, _ := json.Marshal([]any{item, over, fork})
	if bytes.Contains(b, []byte(item.Body)) || bytes.Contains(b, []byte("\"Body\"")) {
		t.Fatal("serialized view leaks body")
	}
	// Overlay parse failures may contain TOML source text internally. Public JSON
	// and errors must contain only the fixed body-free diagnostic vocabulary.
	secret := "PRIVATE_BODY_SENTINEL"
	for _, input := range []string{"body = \"" + secret + "\"", "title = [\"" + secret + "\"", "[slot.x]\nbody = \"" + secret + "\""} {
		ds := l.SaveOverlay("demo-summary", "main", "en", []byte(input))
		assertCode(t, ds, InvalidOverlay)
		encoded, _ := json.Marshal(ds)
		if strings.Contains(string(encoded), secret) || strings.Contains(ds[0].Error(), secret) {
			t.Fatal("diagnostic leaks decode source")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "imports/demo-summary.main.en.json")); err != nil {
		t.Fatal(err)
	}
}

func TestStrictOverlayKeys(t *testing.T) {
	for _, raw := range []string{`body = "x"`, `Body = "x"`, `Title = "x"`, `version = "2"`, `digest = "x"`, `slots = []`, `[slot.x]
required = true`, `[slot.x]
default = "ok"
[slot.x.nested]
default = "bad"`, `[slot]
x = "bad"`, `[slot.x]`, `[slot.".."]
default = "bad"`, `enabled = "false"`, `app_bundle_ids = "app"`, `[[slot.x]]
default = "bad"`} {
		_, ds := ParseOverlay([]byte(raw))
		if len(ds) == 0 {
			t.Errorf("accepted overlay %q", raw)
		}
	}
	o, ds := ParseOverlay([]byte("title = \"\"\nenabled = false\napp_bundle_ids = []\n[slot.x]\ndefault = \"\""))
	noDiagnostics(t, ds)
	if o.Title == nil || o.Enabled == nil || *o.Enabled || o.AppBundleIDs == nil || o.Slot["x"].Default == nil {
		t.Fatal("lost pointer presence")
	}
}

func TestMergeLocalWinsExactlyOneDiagnostic(t *testing.T) {
	l, _ := newLibrary(t)
	dir := libraryFixture(t)
	installSummary(t, l, dir, SourceBundled)
	source := onlyItem(t, l)
	local := Item{ID: source.ID, Body: "local markdown", Title: "local", Enabled: true}
	rows, ds := l.View([]Item{local, local})
	if len(rows) != 1 || rows[0].Source != Local || rows[0].SourceKind != SourceLocal || rows[0].Body != local.Body || rows[0].Provenance != nil || len(ds) != 1 || ds[0].Code != DuplicateID {
		t.Fatalf("bad conflict resolution: %v %v", rows, ds)
	}
	// A bundle without a collision is visible with its provenance and source.
	if source.Source != Bundled || source.SourceKind != SourceBundled || source.Provenance == nil {
		t.Fatal("bundle hidden")
	}
	out, ds := MergeView(nil, []Item{{ID: "same", Title: "imported"}}, []Item{{ID: "same", Title: "bundle"}})
	if len(out) != 1 || out[0].Source != Imported || out[0].Title != "imported" || len(ds) != 1 {
		t.Fatal("imported should precede bundled")
	}
}

func TestOrphanRetainsPinCASAndOverlay(t *testing.T) {
	dir := libraryFixture(t)
	l, root := newLibrary(t)
	installSummary(t, l, dir, SourceFile)
	noDiagnostics(t, l.SaveOverlay("demo-summary", "main", "en", []byte("title = \"keep overlay\"\nenabled = false")))
	before := onlyItem(t, l)
	recordBefore, _ := os.ReadFile(filepath.Join(root, "imports/demo-summary.main.en.json"))
	overlayBefore, _ := os.ReadFile(filepath.Join(root, "overlays/demo-summary.main.en.toml"))
	c := fixture(t)
	c.Solutions = nil
	c.Discovery = nil
	writeCatalog(t, dir, c)
	noDiagnostics(t, l.Refresh(dir))
	rows, ds := l.View(nil)
	assertCode(t, ds, Orphaned)
	if len(rows) != 1 || !rows[0].Orphaned || rows[0].Body != before.Body || rows[0].Enabled || rows[0].Title != "keep overlay" || rows[0].Provenance.Digest != before.Provenance.Digest {
		t.Fatal("revocation discarded pinned state")
	}
	after, _ := os.ReadFile(filepath.Join(root, "imports/demo-summary.main.en.json"))
	overlayAfter, _ := os.ReadFile(filepath.Join(root, "overlays/demo-summary.main.en.toml"))
	if !bytes.Equal(recordBefore, after) || !bytes.Equal(overlayBefore, overlayAfter) {
		t.Fatal("refresh rewrote import/overlay")
	}
	// T1 public parser still rejects empty distributions.
	raw, _ := os.ReadFile(filepath.Join(dir, "catalog.json"))
	if c, ds := ParseCatalog(raw); c != nil || len(ds) == 0 {
		t.Fatal("T1 nonempty contract changed")
	}
}

func TestRefreshAndUpdateLastKnownGood(t *testing.T) {
	dir := libraryFixture(t)
	l, root := newLibrary(t)
	installSummary(t, l, dir, SourceFile)
	old := onlyItem(t, l)
	prior, _ := os.ReadFile(filepath.Join(root, snapshotPath))
	t.Run("bad body preserves snapshot and import", func(t *testing.T) {
		tmpl := fixture(t).Solutions[0].Templates[0]
		body, _ := os.ReadFile(filepath.Join(dir, tmpl.Path))
		defer os.WriteFile(filepath.Join(dir, tmpl.Path), body, 0600)
		os.WriteFile(filepath.Join(dir, tmpl.Path), []byte("PRIVATE_BAD_BODY"), 0600)
		ds := l.Refresh(dir)
		assertCode(t, ds, DigestMismatch)
		ds = l.Import(dir, "demo/writing", "demo-summary", "main", "en", SourceFile)
		assertCode(t, ds, DigestMismatch)
		after, _ := os.ReadFile(filepath.Join(root, snapshotPath))
		if !bytes.Equal(prior, after) || onlyItem(t, l).Body != old.Body {
			t.Fatal("failed verification replaced last-known-good")
		}
		encoded, _ := json.Marshal(ds)
		if bytes.Contains(encoded, []byte("PRIVATE_BAD_BODY")) {
			t.Fatal("body leak")
		}
	})
	t.Run("wrong upstream", func(t *testing.T) {
		c := fixture(t)
		c.Repository.ID = "another-repo"
		writeCatalog(t, dir, c)
		defer writeCatalog(t, dir, fixture(t))
		assertCode(t, l.Refresh(dir), UpstreamMismatch)
		assertCode(t, l.Import(dir, "demo/writing", "demo-summary", "main", "en", SourceFile), UpstreamMismatch)
		if onlyItem(t, l).Orphaned {
			t.Fatal("wrong upstream orphaned pin")
		}
	})
	t.Run("missing and corrupt catalog", func(t *testing.T) {
		defer writeCatalog(t, dir, fixture(t))
		os.Remove(filepath.Join(dir, "catalog.json"))
		assertCode(t, l.Refresh(dir), DanglingReference)
		os.WriteFile(filepath.Join(dir, "catalog.json"), []byte("PRIVATE_BAD_JSON"), 0600)
		assertCode(t, l.Refresh(dir), InvalidJSON)
		if onlyItem(t, l).Orphaned {
			t.Fatal("failed catalog marked orphan")
		}
	})
	t.Run("missing and corrupt snapshot no orphan", func(t *testing.T) {
		defer os.WriteFile(filepath.Join(root, snapshotPath), prior, 0600)
		for _, corrupt := range []bool{false, true} {
			os.Remove(filepath.Join(root, snapshotPath))
			if corrupt {
				os.WriteFile(filepath.Join(root, snapshotPath), []byte("not JSON"), 0600)
			}
			rows, ds := l.View(nil)
			if len(rows) != 1 || rows[0].Orphaned || len(ds) == 0 || rows[0].UpdateStatus != "snapshot_unavailable" {
				t.Fatal("missing/bad snapshot fabricated orphan")
			}
		}
	})
}

func TestExplicitVerifiedUpdateKeepsOverlay(t *testing.T) {
	dir := libraryFixture(t)
	l, _ := newLibrary(t)
	installSummary(t, l, dir, SourceFile)
	noDiagnostics(t, l.SaveOverlay("demo-summary", "main", "en", []byte("title = \"pinned overlay\"")))
	old := onlyItem(t, l)
	c := fixture(t)
	s := &c.Solutions[0]
	s.Version = "2.0"
	tmpl := &s.Templates[0]
	body := []byte("synthetic second version\n")
	tmpl.Digest = Digest(body)
	os.WriteFile(filepath.Join(dir, tmpl.Path), body, 0600)
	name, _ := ContractPath(*tmpl)
	os.Remove(filepath.Join(dir, name))
	writeCatalog(t, dir, c)
	ds := l.Refresh(dir)
	assertCode(t, ds, DegradedNoContract)
	current := onlyItem(t, l)
	if current.UpdateStatus != "update_available" || current.Body != old.Body {
		t.Fatal("refresh silently updated pin")
	}
	ds = l.Import(dir, "demo/writing", "demo-summary", "main", "en", SourceFile)
	assertCode(t, ds, DegradedNoContract)
	rows, ds := l.View(nil)
	assertCode(t, ds, DegradedNoContract)
	if len(rows) != 1 || rows[0].Body != string(body) || rows[0].Title != "pinned overlay" || rows[0].Provenance.Version != "2.0" {
		t.Fatal("explicit update failed or lost overlay")
	}
}

func TestDefaultTextPolicyAndCanonicalRows(t *testing.T) {
	c := fixture(t)
	c.Solutions[0].Rights = "internal"
	c.Solutions[0].Maturity = "exploratory"
	// Duplicate text discovery rows do not multiply canonical templates.
	c.Discovery = append(c.Discovery, c.Discovery[0], Discovery{PackageID: "not-canonical", SolutionID: "phantom", Media: []string{"text"}})
	c.Discovery = append(c.Discovery, Discovery{PackageID: c.Solutions[1].PackageID, SolutionID: c.Solutions[1].ID, Media: []string{"image", "text", "text"}})
	got := DefaultSolutions(c)
	if len(got) != 2 || got[0].ID != "demo-summary" || got[1].ID != "demo-outline" {
		t.Fatalf("policy used search rows, maturity, rights or failed distinct union: %v", got)
	}
	dir := libraryFixture(t)
	writeCatalog(t, dir, c)
	l, _ := newLibrary(t)
	noDiagnostics(t, l.Refresh(dir))
	noDiagnostics(t, l.Import(dir, "demo/writing", "demo-summary", "main", "en", SourceFile))
	assertCode(t, l.Import(dir, "demo/code", "demo-review", "main", "en", SourceFile), DanglingReference)
	rows, ds := l.View(nil)
	noDiagnostics(t, ds)
	if len(rows) != 1 || rows[0].ID != "demo-summary.main.en" {
		t.Fatal("noncanonical or mixed row visible")
	}
	// The real slice has no discovery: modality:text is the only accepted fallback.
	raw, err := os.ReadFile("testdata/real/catalog-slice.json")
	if err != nil {
		t.Fatal(err)
	}
	real, ds := ParseCatalog(raw)
	noDiagnostics(t, ds)
	got = DefaultSolutions(*real)
	if len(got) != 1 || got[0].ID != "tool-use-handoff" || got[0].Rights != "internal" || got[0].Maturity != "exploratory" {
		t.Fatal("real exploratory text solution excluded or no-media solution accepted")
	}
}

func TestAmbiguousVariantsFailClosed(t *testing.T) {
	for _, kind := range []string{"package", "version", "variant", "flattening"} {
		t.Run(kind, func(t *testing.T) {
			dir := libraryFixture(t)
			c := fixture(t)
			s := c.Solutions[0]
			switch kind {
			case "package":
				s.PackageID = "other"
			case "version":
				s.Version = "2"
			case "variant":
				c.Solutions[0].Templates = append(c.Solutions[0].Templates, c.Solutions[0].Templates[0])
				writeCatalog(t, dir, c)
			case "flattening":
				s.ID = "demo-summary.main"
				s.Templates = append([]Template(nil), s.Templates...)
				s.Templates[0].Role = "en"
				s.Templates[0].Locale = "x"
				c.Solutions[0].Templates[0].Locale = "en.x"
			}
			if kind != "variant" {
				c.Solutions = append(c.Solutions, s)
				writeCatalog(t, dir, c)
			}
			l, _ := newLibrary(t)
			assertCode(t, l.Refresh(dir), AmbiguousIdentity)
		})
	}
}

func TestSidecarHeaderOnlyFailsClosed(t *testing.T) {
	for _, kind := range []string{"schema", "binding", "identity", "json", "opaque"} {
		t.Run(kind, func(t *testing.T) {
			dir := libraryFixture(t)
			c := fixture(t)
			tmpl := c.Solutions[0].Templates[0]
			name, _ := ContractPath(tmpl)
			data := map[string]any{"schema_version": ContractSchema, "template_digest": tmpl.Digest, "inputs": []any{map[string]any{"future_type": "T3-owned"}}}
			switch kind {
			case "schema":
				data["schema_version"] = "v999"
			case "binding":
				data["template_digest"] = Digest([]byte("bad"))
			case "identity":
				data["solution_id"] = "another"
			}
			b, _ := json.Marshal(data)
			if kind == "json" {
				b = []byte("PRIVATE_CONTRACT_BAD_JSON")
			}
			os.WriteFile(filepath.Join(dir, name), b, 0600)
			l, _ := newLibrary(t)
			ds := l.Refresh(dir)
			if kind == "opaque" {
				noDiagnostics(t, ds)
				noDiagnostics(t, l.Import(dir, "demo/writing", "demo-summary", "main", "en", SourceFile))
				return
			}
			want := map[string]Code{"schema": UnknownSchema, "binding": DigestMismatch, "identity": UpstreamMismatch, "json": InvalidJSON}[kind]
			assertCode(t, ds, want)
			encoded, _ := json.Marshal(ds)
			if bytes.Contains(encoded, []byte("PRIVATE_CONTRACT")) {
				t.Fatal("contract decoder leaked source")
			}
		})
	}
}

func TestUnsafeLibraryPathsAndSymlinks(t *testing.T) {
	for _, v := range []string{"../escape", "/absolute", `C:\escape`, "..", ".", "bad/name"} {
		l, _ := newLibrary(t)
		assertCode(t, l.SaveOverlay(v, "main", "en", []byte("enabled=false")), UnsafePath)
		assertCode(t, l.Import("unused", "demo", v, "main", "en", SourceFile), UnsafePath)
	}
	for _, kind := range []string{"catalog", "snapshot", "overlay", "overlays-dir", "cas", "imports"} {
		t.Run(kind, func(t *testing.T) {
			dir := libraryFixture(t)
			l, root := newLibrary(t)
			installSummary(t, l, dir, SourceFile)
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "sentinel")
			os.WriteFile(sentinel, []byte("outside untouched"), 0600)
			switch kind {
			case "catalog":
				os.Remove(filepath.Join(dir, "catalog.json"))
				os.Symlink(sentinel, filepath.Join(dir, "catalog.json"))
				assertCode(t, l.Refresh(dir), DanglingReference)
			case "snapshot":
				os.Remove(filepath.Join(root, snapshotPath))
				os.Symlink(sentinel, filepath.Join(root, snapshotPath))
				assertCode(t, l.Refresh(dir), IOError)
				rows, ds := l.View(nil)
				if len(rows) != 1 || rows[0].Orphaned || len(ds) == 0 {
					t.Fatal("unsafe snapshot fabricated orphan")
				}
			case "overlay":
				os.MkdirAll(filepath.Join(root, "overlays"), 0700)
				os.Symlink(sentinel, filepath.Join(root, "overlays/demo-summary.main.en.toml"))
				rows, ds := l.View(nil)
				if len(rows) != 0 {
					t.Fatal("unsafe overlay usable")
				}
				assertCode(t, ds, IOError)
				// Atomic rename replaces a final symlink; it must not modify its target.
				noDiagnostics(t, l.SaveOverlay("demo-summary", "main", "en", []byte("enabled=false")))
			case "overlays-dir":
				os.Symlink(outside, filepath.Join(root, "overlays"))
				assertCode(t, l.SaveOverlay("demo-summary", "main", "en", []byte("enabled=false")), IOError)
			case "cas":
				item := onlyItem(t, l)
				d := item.Provenance.Digest
				name := filepath.Join(root, "objects", "sha256", d[7:9], d[7:])
				os.Remove(name)
				os.Symlink(sentinel, name)
				rows, ds := l.View(nil)
				if len(rows) != 0 {
					t.Fatal("escaped CAS visible")
				}
				assertCode(t, ds, DanglingReference)
				assertCode(t, l.Import(dir, "demo/writing", "demo-summary", "main", "en", SourceFile), CASCorrupt)
			case "imports":
				name := filepath.Join(root, "imports/demo-summary.main.en.json")
				os.Remove(name)
				os.Symlink(sentinel, name)
				assertCode(t, l.Import(dir, "demo/writing", "demo-summary", "main", "en", SourceFile), IOError)
			}
			got, _ := os.ReadFile(sentinel)
			if string(got) != "outside untouched" {
				t.Fatal("write escaped root")
			}
		})
	}
}

func TestDefaultSelectionAndIndependentVariantPins(t *testing.T) {
	dir := libraryFixture(t)
	c := fixture(t)
	s := &c.Solutions[0]
	original := s.Templates[0]
	variants := []Template{original, original, original}
	variants[0].Role = "critic"
	variants[0].Locale = "en"
	variants[1].Role = "main"
	variants[1].Locale = "zh-CN"
	variants[2].Role = "main"
	variants[2].Locale = "en"
	for i := range variants {
		v := &variants[i]
		v.Path = "solutions/writing/demo-summary/prompts/" + v.Role + "." + v.Locale + ".md"
		body := []byte("synthetic variant " + v.Role + " " + v.Locale)
		v.Digest = Digest(body)
		if err := os.WriteFile(filepath.Join(dir, v.Path), body, 0600); err != nil {
			t.Fatal(err)
		}
		name, _ := ContractPath(*v)
		b, _ := json.Marshal(map[string]string{"schema_version": ContractSchema, "template_digest": v.Digest})
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
		c.Discovery = append(c.Discovery, Discovery{PackageID: s.PackageID, SolutionID: s.ID, Role: v.Role, Locale: v.Locale, Media: []string{"text", "text"}})
	}
	s.Templates = variants
	selected := DefaultSelections(c, "zh-CN")
	count := 0
	for _, pick := range selected {
		if pick.Solution.ID == s.ID {
			count++
			if pick.Template.Role != "main" || pick.Template.Locale != "zh-CN" {
				t.Fatal("did not prefer main and requested locale")
			}
		}
	}
	if count != 1 || len(selected) != 3 {
		t.Fatal("discovery multiplied default solution rows")
	}
	selected = DefaultSelections(c, "")
	for _, pick := range selected {
		if pick.Solution.ID == s.ID && pick.Template.Locale != "en" {
			t.Fatal("default locale not preferred")
		}
	}
	writeCatalog(t, dir, c)
	l, _ := newLibrary(t)
	noDiagnostics(t, l.Refresh(dir))
	// Default selection produces one row; explicit variants stay independently
	// addressable and share the same solution identity in trusted provenance.
	noDiagnostics(t, l.Import(dir, s.PackageID, s.ID, "main", "en", SourceFile))
	if rows, ds := l.View(nil); len(rows) != 1 || len(ds) != 0 {
		t.Fatal("default install duplicated rows")
	}
	noDiagnostics(t, l.Import(dir, s.PackageID, s.ID, "critic", "en", SourceFile))
	rows, ds := l.View(nil)
	noDiagnostics(t, ds)
	if len(rows) != 2 || rows[0].ID == rows[1].ID || rows[0].Provenance.SolutionID != rows[1].Provenance.SolutionID {
		t.Fatal("explicit variants not independently pinned/groupable")
	}
	// No-main fallback (as used by graph solutions) remains deterministic under
	// row reordering and prefers the requested locale among available roles.
	c.Solutions = c.Solutions[:1]
	c.Solutions[0].Templates = []Template{{Role: "z", Locale: "en"}, {Role: "a", Locale: "zh-CN"}, {Role: "b", Locale: "en"}}
	pick := DefaultSelections(c, "en")[0]
	if pick.Template.Role != "b" {
		t.Fatal("no-main fallback not deterministic")
	}
	c.Solutions[0].Templates[0], c.Solutions[0].Templates[2] = c.Solutions[0].Templates[2], c.Solutions[0].Templates[0]
	if got := DefaultSelections(c, "en")[0]; got.Template != pick.Template {
		t.Fatal("selection depends on row order")
	}
}

func TestImportRefRejectsCrossRepositoryAndPackageCollision(t *testing.T) {
	dir := libraryFixture(t)
	l, root := newLibrary(t)
	installSummary(t, l, dir, SourceFile)
	prior := onlyItem(t, l)
	c := fixture(t)
	c.Repository.ID = "other-repository"
	c.Solutions[0].PackageID = "different-package"
	c.Discovery[0].PackageID = "different-package"
	writeCatalog(t, dir, c)
	// Simulate a copied but valid foreign snapshot (Refresh itself also refuses
	// this transition). Record provenance is an independent collision guard.
	snapshot, _ := os.ReadFile(filepath.Join(dir, "catalog.json"))
	os.WriteFile(filepath.Join(root, snapshotPath), snapshot, 0600)
	assertCode(t, l.Import(dir, "different-package", "demo-summary", "main", "en", SourceFile), UpstreamMismatch)
	rows, ds := l.View(nil)
	noDiagnostics(t, ds)
	if len(rows) != 1 || rows[0].Orphaned || rows[0].Body != prior.Body || rows[0].UpdateStatus != "snapshot_unavailable" {
		t.Fatal("foreign snapshot modified original import")
	}
}

func TestCorruptOverlayDoesNotReenableImport(t *testing.T) {
	dir := libraryFixture(t)
	l, root := newLibrary(t)
	installSummary(t, l, dir, SourceFile)
	noDiagnostics(t, l.SaveOverlay("demo-summary", "main", "en", []byte("enabled = false")))
	if err := os.WriteFile(filepath.Join(root, "overlays/demo-summary.main.en.toml"), []byte("enabled = false\nbody = \"PRIVATE_BODY\""), 0600); err != nil {
		t.Fatal(err)
	}
	rows, ds := l.View(nil)
	assertCode(t, ds, InvalidOverlay)
	if len(rows) != 0 {
		t.Fatal("corrupt overlay silently exposed enabled import")
	}
}
