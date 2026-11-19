# real catalog slice

`catalog-slice.json` is a **verbatim** two-solution slice of the authoritative catalog built by
`template-registry` (`data/yeisme-prompt-templates/catalog.json` on s101), with its `digest` and each
`solution.digest` recomputed by an **independent** implementation (Python, mirroring
`promptrepo.CanonicalCatalogDigest` / `solutionDigest`).

It exists because synthetic fixtures are authored from the same assumptions as the parser, so they
cannot catch a struct-tag mistake. This slice does: it carries one locale with `aliases` and one
locale with **no** `usage` field, which is exactly what a dropped `omitempty` or a missing `Aliases`
field would change.

Contents are catalog metadata only (titles, summaries, usage notes, tags, paths, digests). No prompt
bodies are included.

Regenerate by slicing the catalog and recomputing digests independently — never by running this
package to produce its own expected values.
