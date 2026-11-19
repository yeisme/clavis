package template

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

const snapshotPath = "catalog.snapshot.json"
const ContractSchema = "promptrepo.template-contract.v0.1"

// ImportRecord pins an immutable body and optional opaque contract. Ref excludes
// version, and carries repository/package/solution/role/locale as separate trusted
// components to prevent a same-filename import from updating another upstream.
// ContractDigest is the raw CAS digest; DeclaredContractDigest is an unverified
// canonical digest header for the future contract layer, never a CAS address.
type ImportRecord struct {
	Ref                    string     `json:"ref"`
	Version                string     `json:"version"`
	Digest                 string     `json:"digest"`
	ContractDigest         string     `json:"contractDigest,omitempty"`
	DeclaredContractDigest string     `json:"declaredContractDigest,omitempty"`
	ImportedAt             time.Time  `json:"importedAt"`
	SourceKind             SourceKind `json:"sourceKind"`
	RepositoryID           string     `json:"repositoryID"`
	PackageID              string     `json:"packageID"`
	SolutionID             string     `json:"solutionID"`
	Role                   string     `json:"role"`
	Locale                 string     `json:"locale"`
	Title                  string     `json:"title"`
	Category               string     `json:"category"`
	Maturity               string     `json:"maturity,omitempty"`
}

// Library requires an existing template root. Every subsequent IO operation is
// relative to its os.Root, including staging files, directories and CAS objects.
// It serializes its own operations; callers must use a single writer per root.
type Library struct {
	root *os.Root
	mu   sync.Mutex
}

func OpenLibrary(directory string) (*Library, error) {
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, diagnostic(IOError, "templates", "cannot open template root")
	}
	return &Library{root: r}, nil
}
func (l *Library) Close() error { return l.root.Close() }

// identityStem is stable across versions and source kinds. Dots are allowed in
// components, so all catalogs/imports also check for ambiguous flattened names.
// No IDs are inferred from discovery titles or search ordering.
func identityStem(solution, role, locale string) (string, error) {
	for _, v := range []string{solution, role, locale} {
		if !safeToken(v) || strings.ContainsAny(v, "\r\n\t") {
			return "", diagnostic(UnsafePath, "identity", "unsafe identity component")
		}
	}
	stem := solution + "." + role + "." + locale
	if !safePath(stem) || len(stem) > 220 {
		return "", diagnostic(UnsafePath, "identity", "unsafe or oversized identity")
	}
	return stem, nil
}

func validateIdentities(c *Catalog) []Diagnostic {
	seen := map[string]bool{}
	for _, s := range c.Solutions {
		for _, t := range s.Templates {
			stem, err := identityStem(s.ID, t.Role, t.Locale)
			if err != nil {
				return []Diagnostic{err.(Diagnostic)}
			}
			if seen[stem] {
				return []Diagnostic{diagnostic(AmbiguousIdentity, stem, "ambiguous solution, version or variant identity")}
			}
			seen[stem] = true
		}
	}
	return nil
}

// readSidecar deliberately ignores inputs. Successful header binding does not
// imply a valid execution contract: T3 must validate canonical digest and inputs.
func readSidecar(r *os.Root, s Solution, t Template) ([]byte, string, []Diagnostic, bool) {
	name, err := ContractPath(t)
	if err != nil {
		return nil, "", []Diagnostic{err.(Diagnostic)}, false
	}
	b, err := r.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", []Diagnostic{diagnostic(DegradedNoContract, name, "contract absent; execution contract is not validated")}, true
	}
	if err != nil {
		return nil, "", []Diagnostic{diagnostic(DanglingReference, name, "contract is inaccessible inside root")}, false
	}
	var h struct {
		Schema         string `json:"schema_version"`
		TemplateDigest string `json:"template_digest"`
		Digest         string `json:"digest"`
		PackageID      string `json:"package_id"`
		SolutionID     string `json:"solution_id"`
		Version        string `json:"version"`
		Role           string `json:"role"`
		Locale         string `json:"locale"`
		TemplatePath   string `json:"template_path"`
	}
	if json.Unmarshal(b, &h) != nil {
		return nil, "", []Diagnostic{diagnostic(InvalidJSON, name, "invalid contract header JSON")}, false
	}
	if h.Schema != ContractSchema {
		return nil, "", []Diagnostic{diagnostic(UnknownSchema, name, "unsupported contract header schema")}, false
	}
	if h.TemplateDigest != t.Digest {
		return nil, "", []Diagnostic{diagnostic(DigestMismatch, name, "contract header does not bind this template digest")}, false
	}
	for _, pair := range [][2]string{{h.PackageID, s.PackageID}, {h.SolutionID, s.ID}, {h.Version, s.Version}, {h.Role, t.Role}, {h.Locale, t.Locale}, {h.TemplatePath, t.Path}} {
		if pair[0] != "" && pair[0] != pair[1] {
			return nil, "", []Diagnostic{diagnostic(UpstreamMismatch, name, "contract header identity differs from template")}, false
		}
	}
	if h.Digest != "" && ValidateDigest(h.Digest) != nil {
		return nil, "", []Diagnostic{diagnostic(InvalidDigest, name, "invalid declared contract digest header")}, false
	}
	return b, h.Digest, nil, true
}

func openDistribution(directory string) (*os.Root, *Catalog, []Diagnostic) {
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, []Diagnostic{diagnostic(IOError, "catalog.json", "cannot open distribution root")}
	}
	b, err := r.ReadFile("catalog.json")
	if err != nil {
		r.Close()
		return nil, nil, []Diagnostic{diagnostic(DanglingReference, "catalog.json", "catalog missing or inaccessible inside root")}
	}
	c, ds := parseCatalog(b, true)
	if c != nil {
		ds = append(ds, validateIdentities(c)...)
		if len(ds) > 0 {
			c = nil
		}
	}
	if c == nil {
		r.Close()
		return nil, nil, ds
	}
	return r, c, ds
}

func (l *Library) snapshot() (*Catalog, []Diagnostic) {
	b, err := l.root.ReadFile(snapshotPath)
	if err != nil {
		return nil, []Diagnostic{diagnostic(DanglingReference, snapshotPath, "snapshot missing or inaccessible inside root")}
	}
	c, ds := parseCatalog(b, true)
	if c != nil {
		ds = append(ds, validateIdentities(c)...)
		if len(ds) > 0 {
			c = nil
		}
	}
	return c, ds
}

// Refresh verifies the whole distribution before replacing the snapshot. It
// never updates an import or discards overlays. A valid empty snapshot revokes
// the last row; failed/missing/wrong-upstream snapshots cannot mark orphans.
func (l *Library) Refresh(directory string) []Diagnostic {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, c, ds := openDistribution(directory)
	if c == nil {
		return ds
	}
	defer r.Close()
	if old, err := l.root.ReadFile(snapshotPath); err == nil {
		previous, pds := parseCatalog(old, true)
		if previous == nil {
			return pds
		}
		if previous.Repository.ID != c.Repository.ID {
			return []Diagnostic{diagnostic(UpstreamMismatch, snapshotPath, "snapshot belongs to another upstream")}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return []Diagnostic{diagnostic(IOError, snapshotPath, "cannot inspect previous snapshot")}
	}
	failed := false
	for _, s := range c.Solutions {
		for _, t := range s.Templates {
			b, err := r.ReadFile(t.Path)
			if err != nil {
				ds = append(ds, diagnostic(DanglingReference, t.Path, "template missing or inaccessible inside root"))
				failed = true
			} else if err := VerifyDigest(t.Digest, b); err != nil {
				ds = append(ds, digestDiagnostic(err, t.Path))
				failed = true
			}
			_, _, sd, ok := readSidecar(r, s, t)
			ds = append(ds, sd...)
			if !ok {
				failed = true
			}
		}
	}
	if failed {
		return ds
	}
	// Serialize only known catalog fields. Unknown payloads never enter snapshots.
	b, err := json.Marshal(c)
	if err != nil {
		return append(ds, diagnostic(InvalidJSON, snapshotPath, "cannot encode verified catalog"))
	}
	if err = l.atomicWrite(snapshotPath, b); err != nil {
		ds = append(ds, err.(Diagnostic))
	}
	return ds
}

func validSource(k SourceKind) bool {
	return k == SourcePromptrepo || k == SourceFile || k == SourceBundled || k == SourceGit || k == SourceRegistry
}
func recordRef(r ImportRecord) string {
	b, _ := json.Marshal([]string{r.RepositoryID, r.PackageID, r.SolutionID, r.Role, r.Locale})
	return "promptrepo:" + string(b)
}

// Import explicitly installs/updates one canonical variant at the current
// verified snapshot. Existing provenance must match exactly. CAS and the prior
// record remain usable if any body/header/write check fails.
func (l *Library) Import(directory, packageID, solutionID, role, locale string, kind SourceKind) []Diagnostic {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !validSource(kind) {
		return []Diagnostic{diagnostic(InvalidCatalog, "sourceKind", "unsupported import source kind")}
	}
	stem, err := identityStem(solutionID, role, locale)
	if err != nil {
		return []Diagnostic{err.(Diagnostic)}
	}
	snap, ds := l.snapshot()
	if snap == nil {
		return ds
	}
	r, c, ds := openDistribution(directory)
	if c == nil {
		return ds
	}
	defer r.Close()
	if c.Repository.ID != snap.Repository.ID || c.Digest != snap.Digest {
		return []Diagnostic{diagnostic(UpstreamMismatch, "catalog.json", "distribution does not match verified snapshot")}
	}
	var foundS *Solution
	var foundT *Template
	// Consult the stored full index, never a caller's discovery/search rows.
	for _, s := range DefaultSolutions(*snap) {
		if s.PackageID == packageID && s.ID == solutionID {
			for _, t := range s.Templates {
				if t.Role == role && t.Locale == locale {
					ss, tt := s, t
					foundS = &ss
					foundT = &tt
				}
			}
		}
	}
	if foundT == nil {
		return []Diagnostic{diagnostic(DanglingReference, stem, "no eligible canonical text template at this identity")}
	}
	s, t := *foundS, *foundT
	rec := ImportRecord{Version: s.Version, Digest: t.Digest, ImportedAt: time.Now().UTC(), SourceKind: kind, RepositoryID: c.Repository.ID, PackageID: s.PackageID, SolutionID: s.ID, Role: t.Role, Locale: t.Locale, Title: s.Locales[locale].Title, Category: s.Category, Maturity: s.Maturity}
	rec.Ref = recordRef(rec)
	name := "imports/" + stem + ".json"
	if old, err := l.root.ReadFile(name); err == nil {
		prior, e := decodeRecord(old, stem)
		if e != nil {
			return []Diagnostic{e.(Diagnostic)}
		}
		if prior.Ref != rec.Ref {
			return []Diagnostic{diagnostic(UpstreamMismatch, name, "import identity belongs to another upstream")}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return []Diagnostic{diagnostic(IOError, name, "cannot inspect previous import")}
	}
	b, err := r.ReadFile(t.Path)
	if err != nil {
		return []Diagnostic{diagnostic(DanglingReference, t.Path, "template missing or inaccessible inside root")}
	}
	if err = VerifyDigest(t.Digest, b); err != nil {
		return []Diagnostic{digestDiagnostic(err, t.Path)}
	}
	sidecar, declared, ds, ok := readSidecar(r, s, t)
	if !ok {
		return ds
	}
	cas := CAS{root: l.root}
	if _, err = cas.Put(t.Digest, b); err != nil {
		return append(ds, err.(Diagnostic))
	}
	if sidecar != nil {
		rec.ContractDigest = Digest(sidecar)
		rec.DeclaredContractDigest = declared
		if _, err = cas.Put(rec.ContractDigest, sidecar); err != nil {
			return append(ds, err.(Diagnostic))
		}
	}
	encoded, _ := json.Marshal(rec)
	if err = l.atomicWrite(name, encoded); err != nil {
		ds = append(ds, err.(Diagnostic))
	}
	return ds
}

func decodeRecord(b []byte, stem string) (ImportRecord, error) {
	var r ImportRecord
	bad := func() (ImportRecord, error) {
		return ImportRecord{}, diagnostic(InvalidCatalog, "imports/"+stem+".json", "invalid import metadata")
	}
	if json.Unmarshal(b, &r) != nil {
		return bad()
	}
	actual, err := identityStem(r.SolutionID, r.Role, r.Locale)
	if err != nil || actual != stem || r.RepositoryID == "" || r.PackageID == "" || r.Version == "" || r.Ref != recordRef(r) || !validSource(r.SourceKind) || ValidateDigest(r.Digest) != nil || (r.ContractDigest != "" && ValidateDigest(r.ContractDigest) != nil) {
		return bad()
	}
	return r, nil
}

func (l *Library) readObject(digest string) ([]byte, error) {
	if err := ValidateDigest(digest); err != nil {
		return nil, err
	}
	name := path.Join("objects", "sha256", digest[7:9], digest[7:])
	b, err := l.root.ReadFile(name)
	if err != nil {
		return nil, diagnostic(DanglingReference, name, "CAS object missing or inaccessible inside root")
	}
	if err = VerifyDigest(digest, b); err != nil {
		return nil, digestDiagnostic(err, name)
	}
	return b, nil
}

// View retains revoked imports. Only a valid snapshot of the SAME repository
// may label an item orphaned. No snapshot or a corrupt one yields diagnostics,
// not a fabricated deletion. Returned bodies/defaults are detached from disk.
func (l *Library) View(local []Item) ([]Item, []Diagnostic) {
	l.mu.Lock()
	defer l.mu.Unlock()
	snap, ds := l.snapshot()
	f, err := l.root.Open("imports")
	if errors.Is(err, fs.ErrNotExist) {
		out, md := MergeView(local, nil, nil)
		return out, append(ds, md...)
	}
	if err != nil {
		out, md := MergeView(local, nil, nil)
		return out, append(append(ds, diagnostic(IOError, "imports", "cannot open imports inside root")), md...)
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return nil, append(ds, diagnostic(IOError, "imports", "cannot read imports"))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var imported, bundled []Item
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if !safePath(name) {
			ds = append(ds, diagnostic(UnsafePath, "imports", "unsafe import filename"))
			continue
		}
		stem := strings.TrimSuffix(name, ".json")
		b, err := l.root.ReadFile("imports/" + name)
		if err != nil {
			ds = append(ds, diagnostic(DanglingReference, "imports/"+name, "import is inaccessible inside root"))
			continue
		}
		rec, err := decodeRecord(b, stem)
		if err != nil {
			ds = append(ds, err.(Diagnostic))
			continue
		}
		body, err := l.readObject(rec.Digest)
		if err != nil {
			ds = append(ds, err.(Diagnostic))
			continue
		}
		if rec.ContractDigest != "" {
			if _, err = l.readObject(rec.ContractDigest); err != nil {
				ds = append(ds, err.(Diagnostic))
				continue
			}
		} else {
			ds = append(ds, diagnostic(DegradedNoContract, stem, "contract absent; execution contract is not validated"))
		}
		item := Item{ID: stem, Trigger: "/" + rec.SolutionID, Title: rec.Title, Category: rec.Category, Body: string(body), Enabled: true, SourceKind: rec.SourceKind, Maturity: rec.Maturity, Provenance: &rec, UpdateStatus: "current", ContractStatus: "header_only_unvalidated"}
		if rec.ContractDigest == "" {
			item.ContractStatus = "degraded_no_contract"
		}
		if snap != nil && snap.Repository.ID == rec.RepositoryID {
			present := false
			for _, s := range snap.Solutions {
				if s.PackageID == rec.PackageID && s.ID == rec.SolutionID {
					for _, t := range s.Templates {
						if t.Role == rec.Role && t.Locale == rec.Locale {
							present = true
							if t.Digest != rec.Digest || s.Version != rec.Version {
								item.UpdateStatus = "update_available"
							}
						}
					}
				}
			}
			if !present {
				item.Orphaned = true
				item.UpdateStatus = "orphaned"
				ds = append(ds, diagnostic(Orphaned, stem, "upstream variant revoked; import and overlay retained"))
			}
		} else {
			item.UpdateStatus = "snapshot_unavailable"
		}
		overlay, err := l.root.ReadFile("overlays/" + stem + ".toml")
		if err == nil {
			o, ods := ParseOverlay(overlay)
			ds = append(ds, ods...)
			if len(ods) != 0 {
				// A corrupt disabled overlay must not silently re-enable a row.
				continue
			}
			item = ApplyOverlay(item, o)
		} else if !errors.Is(err, fs.ErrNotExist) {
			ds = append(ds, diagnostic(IOError, "overlays/"+stem+".toml", "overlay inaccessible inside root"))
			continue
		}
		if rec.SourceKind == SourceBundled {
			bundled = append(bundled, item)
		} else {
			imported = append(imported, item)
		}
	}
	out, md := MergeView(local, imported, bundled)
	return out, append(ds, md...)
}

// atomicWrite never follows a destination symlink: rename replaces its directory
// entry. Parent directory resolution, temp creation and rename use the same Root.
func (l *Library) atomicWrite(name string, b []byte) error {
	if !safePath(name) {
		return diagnostic(UnsafePath, "write", "unsafe output path")
	}
	dir := path.Dir(name)
	if err := l.root.MkdirAll(dir, 0700); err != nil {
		return diagnostic(IOError, name, "cannot create output directory inside root")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return diagnostic(IOError, name, "cannot create staging identity")
	}
	tmp := path.Join(dir, ".tmp-"+hex.EncodeToString(random[:]))
	f, err := l.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return diagnostic(IOError, name, "cannot create staging file inside root")
	}
	defer l.root.Remove(tmp)
	_, we := f.Write(b)
	if we == nil {
		we = f.Sync()
	}
	ce := f.Close()
	if we != nil || ce != nil {
		return diagnostic(IOError, name, "cannot write staging file")
	}
	if err = l.root.Rename(tmp, name); err != nil {
		return diagnostic(IOError, name, "cannot publish staged file inside root")
	}
	return nil
}
