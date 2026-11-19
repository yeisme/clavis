# Offline template library (T2)

`OpenLibrary(existingDirectory)` owns an `os.Root`; close the library when done.
Use one writer/library per root. No network, configuration migration, renderer,
contract input interpretation or UI is included.

- `Refresh(distributionDirectory)` validates the catalog, canonical identities,
  bodies and opaque sidecar headers before atomically replacing
  `catalog.snapshot.json`. It does **not** update installed pins. A valid empty
  catalog revokes the final variant; missing/corrupt/foreign snapshots cannot
  fabricate orphan status. The public T1 `ParseCatalog` still rejects emptiness.
- `DefaultSelections(catalog, locale)` selects one variant per pure-text solution:
  main role first, requested/default/en locale next, lexical fallback for no-main
  solutions. Selection uses canonical solutions/templates, never discovery rows.
  The distinct media union uses the complete discovery index keyed by package
  and solution; only when discovery is absent do `modality:` tags supply media.
  Rights and maturity are not admission gates.
- `Import(directory, packageID, solutionID, role, locale, sourceKind)` explicitly
  installs/updates an eligible variant from the verified snapshot. Pass the
  fields from a default selection, or explicitly select another canonical
  variant. Filenames and globally stable view IDs are
  `<solution>.<role>.<locale>` (extensions `.json` and `.toml`). Ambiguous stems
  across packages, versions or dotted components are rejected, not guessed.
  Separately installed variants are independent rows, groupable by the trusted
  provenance `repositoryID/packageID/solutionID`, not repeated discovery hits.
- Bodies and raw contract bytes use T1's sharded
  `objects/sha256/<first-two-hex>/<hex>` CAS. Imports keep ref/version/digest,
  import time, source kind and the minimum display/identity metadata. Updates
  compare provenance to prevent cross-repository/package replacement. A failed
  update retains the old import; unreferenced immutable CAS objects may remain.
- `SaveOverlay` accepts only `trigger`, `title`, `category`, `app_bundle_ids`,
  `enabled` and `[slot.<name>] default`. Explicit false/empty values survive.
  Bodies are never writable through overlays. Slot names/defaults are opaque;
  T3 must bind/filter them against validated contract inputs.
- `View` + `MergeView` return detached in-memory bodies, omitted from JSON.
  Local wins over imported, then bundled, with exactly one `duplicate_id` per
  colliding ID. `source` is the merged classification; `sourceKind` retains the
  concrete import origin and `maturity` is display metadata only. Revoked imports
  retain CAS and overlays with `orphaned` status.
  `config.ConfigStore.TemplateView` adapts already merged `Current().Prompts`;
  it never reads/writes legacy prompts TOML/Markdown or changes precedence.
- `Fork` returns an in-memory `local:<id>` copy with no provenance and
  `updateStatus=no_updates`. It never persists files or rewrites local config.

A sidecar's `contractDigest` is its **raw CAS digest**, not the optional
`declaredContractDigest` canonical header. Header checks bind schema and body
SHA-256, plus any supplied identity fields. They do not verify the canonical
contract digest or parse inputs: `contractStatus=header_only_unvalidated` is
explicitly **not** execution authorization. Missing sidecars are importable with
`degraded_no_contract`. T3 owns full contract validation and rendering.

All new filesystem operations are root-relative, including staged temp+rename
writes and reused T1 CAS operations. Diagnostics have stable JSON
`code/path/message` fields and fixed body-free decode errors. Digest verification
is corruption detection, not authentication. There is no multi-file transaction
or cross-process lock; each snapshot/import/overlay replacement is atomic.
