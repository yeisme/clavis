# Visual language

Clavis has three visual presets and one design-token layer. This document is the
contract: if a view hardcodes a number or re-derives a decision, it is a bug.

## Presets

| Preset | Geometry | Material | Shaders |
| --- | --- | --- | --- |
| `classic` | flat, no perspective | glass, opaque fallback under Reduce Transparency | never |
| `liquid_glass` | flat, no perspective | glass + elevation | GPU when allowed |
| `spatial_3d` (default) | per-phase stage camera + oblique result rows | glass + elevation | GPU when allowed |

`stage_intensity` scales `spatial_3d` from `0` (flat geometry, same materials)
to `1` (full). `oblique_rows` turns the row fan on or off independently.

## Stage resolution

`resolveStagePlan(stageInputs) stagePlan` in `uiapp/stage.go` is the **only**
place that decides whether a frame is 3D or shaded. It is a pure function — no
`*ui.Context`, no globals, no clock — which is what makes the matrix testable.

Derived values with `k = clamp(stage_intensity, 0, 1)` (default `0.7`):

| Field | `spatial_3d` | `liquid_glass` | `classic` |
| --- | --- | --- | --- |
| `Yaw` (per phase step) | `7k`° | 0 | 0 |
| `Pitch` | `2.5k`° | 0 | 0 |
| `Perspective` | 1000 DIP | 0 | 0 |
| `RowSkew` (max) | `5k`° (if `oblique_rows`) | 0 | 0 |
| `Parallax` | `0.55k` | 0 | 0 |
| `Specular` | `0.7k` | `0.7k` | 0 |

Phase positions: Leader `−1`, Omnibox `0`, AI `+1`; camera `yaw = Yaw × index`.
At `k = 0` every angle is zero while `Enabled3D` stays true, so the materials
still apply.

### Gates (first match wins)

| Condition | Effect |
| --- | --- |
| `animations = reduced`/`off`, or system Reduce Motion | identity plan, `Frozen` |
| `preset = classic` | identity plan, `Frozen` |
| **IME composing** | `Frozen`, all angles/parallax/specular zeroed — typing is always planar and instant |
| `High Contrast` | `Pitch = 0`, `Specular = 0` (yaw and row skew kept) |
| `Reduce Transparency` | shaders off; 3D kept |
| software painter (`!p.Accelerated()`) | shaders off; 3D kept |

`Frozen` means transforms are applied as plain values (no spring) and the
surface must not request another animation frame (`stageRequestsAnimationFrames`).

### The oblique result list

Each row is transformed as **one element** so its fill, text, icon and hit
geometry stay coherent — never rotate a child layer separately.

- the **selected row is identity** (0°, no Z), so glyphs stay crisp;
- neighbours take `clamp((index − selected) × 1.2, ±RowSkew)` degrees and
  `z = −min(|index − selected|, 6) × 2`;
- at `RowSkew ≤ 5°` the vertical foreshortening is ~0.4% — imperceptible;
- `Frozen` or `RowSkew = 0` short-circuits to the untransformed element.

Row geometry is renderer-independent: it never consults shader capability.

## Shader budget

**At most 3 shader-eligible surfaces per frame**: the AI header glow, the
streaming orb, and the sheen on the one hovered card. `shaderDrawCount` counts
*eligibility*, not successful draws, so the budget binds on a software painter
too. `TestT6ShaderDrawBudget` fails at 4.

## Tokens

`uiapp/tokens.go`. Views take every number from here.

### Spacing (DIP)

`space0 0` · `space1 1` · `space2 2` · `space4 4` · `space6 6` · `space8 8` ·
`space10 10` · `space12 12` · `space14 14` · `space16 16` · `space18 18` ·
`space20 20` · `space24 24` · `space32 32`

### Radii

`radius4 4` (marker) · `radiusKeycap 5` · `radiusChip 6` · `radiusTile 7` ·
`radius8 8` (inline controls) · `radiusRow 9` · `radiusCard 10` ·
`radiusSegment 11` (pill segments) · `radius12 12` (panels) · `radiusPill 999`

### Type (pt) and line height

`typeMicro 10` · `typeMeta 11` · `typeLabel 12` · `typeBody 13` ·
`typeTitle 14` · `typeSearch 16` · `typeGlyph 17` · `typeHero 20`
Line height: `lineTight 1.25` for headings and single-line labels,
`lineProse 1.55` for prose.

### Elevation

| Level | Offset | Blur | Light alpha | Dark alpha |
| --- | --- | --- | --- | --- |
| `elevNone` | — | — | 0 | 0 |
| `elev1` | 0,1 | 2 | .10 | .28 |
| `elev2` | 0,3 | 10 | .14 | .34 |
| `elev3` | 0,8 | 24 | .18 | .40 |
| `elev4` | 0,16 | 40 | .22 | .46 |

`surfaceHighlight(t)` is the 1px inset top highlight that distinguishes a raised
card from the surface behind it.

### Opacity

`alphaFaint .06` (resting washes) · `alphaSubtle .14` (selected/status tints) ·
`alphaSoft .24` (chosen row, dark) · `alphaHalf .50` (keycap/chip outlines) ·
`alphaStrong .70` (focus rings, emphasis)

Opacity is **never** written as `spaceN/spaceM`: that ties a tint to the spacing
ramp and reads as geometry. `TestOpacityIsNotExpressedAsSpacing` enforces it.

### Density

`headerHeight 40` · `searchRowHeight 52` · `rowHeightOmnibox 44` ·
`footerHeight 44` · `controlHeight 28` · `cardPadX 12` · `cardPadY 10` ·
`cardGap 10` · `railWidth 216`

`uiapp/layout.go` derives the panel sizes from these, so a density change is one
edit plus the layout tests:

- Omnibox height `= 152 + 48n`, clamped `[320, 720]`
- Leader height `= 106 + ⌈n/2⌉ × 60`, clamped `[172, 480]`
- AI streaming `520` (multi-turn `580`); HUD `headerHeight + space16 = 56`

### Dialog skeleton

`uiapp/kit_dialog.go` gives the prompt composer, the first-run guide and the
selection bar one skeleton:

- **Shell.** `kitFloatingShell`: glass only at the edge, the body washed with
  `glassBodyTint`, and a hairline. Reduce Transparency gives an opaque
  background; Increase Contrast a 1.5px border.
- **Title bar.** `kitDialogHeader`: `headerHeight` tall, with a neutral icon,
  a `typeTitle`/600 title, a secondary line and `kitMetaChip` meta, then a
  hairline.
- **Body.** `kitDialogBody`: `dialogPadY 12` × `dialogPadX 14`, `cardGap`.
- **Footer.** `kitDialogFooter`: a hairline, then `footerHeight` tall. The hint
  takes the leading space. Buttons sit at the trailing edge in macOS order:
  others, then Cancel, then the primary last.

No accent fills sit in title bars, chips or tags. The selection bar sizes each
action to its label (`PreferredSelectionBarWidth` uses the view's own metrics).
`TestDialogsShareOneSkeleton` and `TestSelectionBarFitsItsLabels` check this on
real renders.

## Accessibility

- **Contrast is derived, not chosen.** `accentInk`/`accentGlyph`/`matchMark`/
  `selectedTileGlyph` blend toward the text colour until they clear 4.5:1 (text)
  or 3:1 (glyphs), so a yellow or graphite system accent stays readable.
  `TestTokenContrast` checks every derived colour, on elevation backgrounds and
  on the card surface, for light/dark × {yellow, graphite, pink} accents.
- **Secondary lines read at 4.5:1 too.** `secondaryInk` is `TextMuted` pushed
  toward the text colour until it clears 4.5:1 on the chosen row's tint (the
  worst case); row subtitles and the clipboard "source · time · N lines" line use
  it. `TestOmniboxSelectionIsEdgeBarNotFlood` re-measures it on real rendered
  pixels, light and dark.
- **The chosen Omnibox row is an edge bar, not a flood.** A 2px
  `rowSelectionBar` flush with the row's leading edge (inset `space10` top and
  bottom) over a faint `rowSelectionFill` (accent .08 light / .14 dark). There is
  no accent outline, no accent icon tile and no bolder title. The bar is held to
  3:1 against the tint, so a pale system accent still reads. Controls that use
  `selectionFill`/`selectionEdge` (Leader list, settings, segmented controls)
  keep their own style.
- **On floating windows the accent marks selection only.** That covers the
  chosen candidate (a solid fill, as in the native IME), the IME pill's live
  segment and the chosen row.
  - Neutral: the Leader brand chip, the layer and info icons in pills, and an
    unselected AI candidate's ✦.
  - Status icons keep their own colors (success, danger).
  - `TestFloatingSurfacesKeepAccentForSelection` reads this off real renders.
- **Candidate widths hold in Chinese.** Under `applyTextLocale` the Han face
  draws 1.013–1.021 em a glyph. The CJK estimate therefore rounds `fontSize ×
  1.025` up; a 1 em estimate made a full page shrink every word ("输入…").
  `TestCandidateHorizontalCutsOnlyPlannedTexts` checks it at 1× and 2× in
  both languages.
- **Reduce Motion** → identity plan: no rotation, no slide, opacity only.
- **Reduce Transparency** → opaque surface + border, shaders off.
- **High Contrast** → no pitch, no specular, heavier borders.
- **IME composition always wins**: the stage freezes flat for the whole preedit.

## Enforcement

| Guard | What it stops |
| --- | --- |
| `TestStyleLiteralGuard` | new raw `Radius(n)` / `FontSize(n)` / `Padding(n` literals (per-file ratchet) |
| `TestOpacityIsNotExpressedAsSpacing` | `spaceN/spaceM` used as alpha |
| `TestTokenContrast` | unreadable derived colours, including hostile system accents |
| `TestStageResolutionMatrix` | any drift in the gate table above |
| `TestT6ShaderDrawBudget` | a fourth shader-eligible surface |
| `TestSpatialComponentsAreWiredIntoProductionSurfaces` | 3D/shader components existing only in tests |
| `TestVisualSnapshots` (opt-in) | anything that renders blank/flat across preset × motion × theme × language |

## Cost

`BenchmarkFrame*`, 16 ms/frame budget:

| Frame | ns/op |
| --- | --- |
| HUD | 5 431 |
| Leader | 36 933 |
| Omnibox (classic) | 91 242 |
| Omnibox (spatial_3d) | 97 429 |

The spatial look costs **+6.8%** per Omnibox frame (~0.6% of the budget). A
regression past ~35% over classic, or a fourth shader surface, is a bug.
