package uiapp

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/yeisme/clavis/pkg/core/engine"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

// MediaSearchView renders a standalone, keyboard-first Media Search & Async
// Preview workspace covering all six media families (Image, Video, Audio,
// Document, Archive, File) plus explicit Unavailable fallback state.
func (a *AppController) MediaSearchView(c *ui.Context) {
	cfg := a.Config()
	theme := resolveAppearanceTheme(c, cfg.Main.Appearance.Theme)
	c.SetTheme(theme)

	allItems := a.MediaSearchItems()
	a.renderMediaSearchSurface(c, theme, allItems)
}

func (a *AppController) renderMediaSearchSurface(c *ui.Context, theme *ui.Theme, allItems []engine.OmniboxItem) {
	category := a.MediaSearchCategory()
	query := a.MediaSearchQuery()
	visible := FilterMediaItems(allItems, category, query)
	counts := MediaCategoryCounts(allItems)

	selIdx := a.MediaSearchSelectedIndex()
	if len(visible) == 0 {
		selIdx = 0
	} else if selIdx >= len(visible) {
		selIdx = len(visible) - 1
		a.SetMediaSearchSelectedIndex(selIdx, len(visible))
	}

	st := a.mediaSearchState()
	st.mu.Lock()
	statusMsg := st.statusMsg
	st.mu.Unlock()

	applyTextLocale(ui.Column(c).Fill().Background(theme.Background).Padding(space12, space14).Gap(space8)).Children(func() {
		// 1. Keyboard-first Search Header (IME-aware).
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			renderSemanticIcon(c, "search", IconSize16, secondaryInk(theme))
			q := query
			in := ui.TextInput(c.Key("media-search-input"), &q).
				AutoFocus().
				Search().
				Placeholder(i18n.T("media.search.placeholder")).
				Label(i18n.T("media.search.label")).
				FontSize(typeSearch).
				Grow(1)

			in.ObserveInput(func(ev ui.InputEvent) {
				st.mu.Lock()
				switch ev.Kind {
				case ui.InputCompose:
					st.isComposing = ev.Text != ""
				case ui.InputText, ui.InputCommand:
					st.isComposing = false
				}
				st.mu.Unlock()
			})
			in.HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind != ui.InputKeyDown {
					return false
				}
				return a.HandleMediaSearchKey(ev.Key, ev.Mods, visible)
			})
			if in.Changed() && q != query {
				a.SetMediaSearchQuery(q)
			}

			kitStatusPill(c, theme, fmt.Sprintf("%d / %d", len(visible), len(allItems)), chipNeutral)
		})

		// 2. Six-Category Media Filter Bar (⌘1..⌘7 / Tab).
		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			for _, entry := range RegisteredMediaFilters() {
				e := entry
				active := category == e.Category
				cnt := counts[e.Category]
				pill := ui.ButtonBase(c).Key("media-filter:"+string(e.Category)).
					Height(space24+space2).Padding(space2, space8).Radius(radiusChip).FocusRing(false)
				if active {
					pill.Background(theme.Accent)
				} else {
					pill.Background(theme.Surface).Border(space1, hairline(theme))
				}
				pill.Children(func() {
					ui.Row(c).Gap(space4).AlignItems(ui.Center).Children(func() {
						iconColor := secondaryInk(theme)
						textColor := theme.Text
						metaColor := theme.TextMuted
						if active {
							iconColor = theme.AccentText
							textColor = theme.AccentText
							metaColor = theme.AccentText
						}
						renderSemanticIcon(c, e.Icon, IconSize16, iconColor)
						ui.Text(c, e.LabelFunc()).FontSize(typeMeta).FontWeight(600).TextColor(textColor).SingleLine()
						ui.Text(c, fmt.Sprintf("%d", cnt)).FontSize(typeMicro).TextColor(metaColor).SingleLine()
					})
				})
				if pill.Clicked() {
					a.SetMediaSearchCategory(e.Category)
				}
			}
		})

		kitDivider(c, theme)

		// 3. Split Workspace: Left Keyboard List + Right Async Preview Pane.
		ui.Row(c).FillWidth().Grow(1).MinHeight(space0).Gap(space10).Children(func() {
			// Left list pane (42%).
			ui.Column(c).FillHeight().Grow(2).MinWidth(space0).Gap(space4).Children(func() {
				if len(visible) == 0 {
					kitCard(c, theme, cardInset, func() {
						ui.Column(c).FillWidth().Gap(space6).Padding(space12, space8).Children(func() {
							ui.Text(c, i18n.T("media.search.empty_title")).
								FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).Wrap()
							ui.Text(c, i18n.T("media.search.empty_hint")).
								FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
						})
					})
					return
				}
				ui.Scroll(c).Fill().Children(func() {
					ui.Column(c).FillWidth().Gap(space4).Children(func() {
						for i, item := range visible {
							idx, it := i, item
							a.renderMediaSearchRow(c, theme, idx, it, idx == selIdx, len(visible))
						}
					})
				})
			})

			ui.Box(c).Width(space1).FillHeight().Background(hairline(theme))

			// Right async preview pane (58%).
			ui.Column(c).FillHeight().Grow(3).MinWidth(space0).Gap(space6).Children(func() {
				if len(visible) == 0 || selIdx < 0 || selIdx >= len(visible) {
					a.renderMediaEmptyPreview(c, theme)
					return
				}
				a.renderMediaAsyncPreviewPane(c, theme, visible[selIdx])
			})
		})

		kitDivider(c, theme)

		// 4. Keyboard-first Footer.
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			prefix := modPrefix()
			renderFooterHints(c, theme, []footerHint{
				{keys: "↑↓", caption: i18n.T("media.footer.select")},
				{keys: "Tab / " + prefix + "1–7", caption: i18n.T("media.footer.filter")},
				{keys: "↵", caption: i18n.T("media.footer.paste")},
				{keys: prefix + "C", caption: i18n.T("media.action.copy_path")},
				{keys: prefix + "R", caption: i18n.T("media.action.recheck")},
				{keys: "esc", caption: i18n.T("media.footer.reset")},
			})
			ui.Spacer(c)
			if statusMsg != "" {
				kitStatusPill(c, theme, statusMsg, chipNeutral)
			}
		})
	})
}

func (a *AppController) renderMediaSearchRow(
	c *ui.Context,
	theme *ui.Theme,
	idx int,
	item engine.OmniboxItem,
	selected bool,
	total int,
) {
	if item.ClipboardMedia == nil {
		return
	}
	m := *item.ClipboardMedia
	cat := ClassifyClipboardMedia(m)
	snap := a.MediaProbe(m)

	row := ui.ButtonBase(c).Key(fmt.Sprintf("media-row:%d:%s", idx, item.ID)).
		FillWidth().Padding(space6, space8).Radius(radiusRow).FocusRing(false)
	if selected {
		row.Background(selectionFill(theme)).Border(space1, selectionEdge(theme))
	} else {
		row.Background(theme.Surface).Border(space1, hairline(theme))
	}

	title := strings.TrimSpace(item.Title)
	path := mediaPrimaryPath(m)
	if title == "" && path != "" {
		title = filepath.Base(path)
	}
	if title == "" {
		title = MediaCategoryLabel(cat)
	}

	metaParts := []string{MediaCategoryLabel(cat)}
	if ext := mediaPathExt(path); ext != "" {
		metaParts = append(metaParts, strings.ToUpper(strings.TrimPrefix(ext, ".")))
	}
	size := snap.ByteSize
	if size <= 0 {
		size = m.ByteSize
	}
	if size > 0 {
		metaParts = append(metaParts, humanBytes(size))
	}

	row.Children(func() {
		ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
			kitIconTile(c, theme, MediaCategoryIcon(cat), selected)
			ui.Column(c).Grow(1).MinWidth(space0).Gap(space1).Children(func() {
				ui.Text(c, title).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine()
				ui.Text(c, strings.Join(metaParts, " · ")).FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
			})
			if snap.Status == MediaProbeUnavailable {
				kitStatusPill(c, theme, i18n.T("media.status.unavailable"), chipWarning)
			} else if snap.Status == MediaProbeLoading {
				kitStatusPill(c, theme, i18n.T("media.status.loading"), chipNeutral)
			}
		})
	})

	if row.Clicked() {
		a.SetMediaSearchSelectedIndex(idx, total)
	}
}

func (a *AppController) renderMediaEmptyPreview(c *ui.Context, theme *ui.Theme) {
	kitCard(c, theme, cardInset, func() {
		ui.Column(c).FillWidth().Padding(space12, space10).Gap(space4).Children(func() {
			ui.Text(c, i18n.T("media.preview.empty_hint")).
				FontSize(typeLabel).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
		})
	})
}

func (a *AppController) renderMediaAsyncPreviewPane(c *ui.Context, theme *ui.Theme, item engine.OmniboxItem) {
	if item.ClipboardMedia == nil {
		a.renderMediaEmptyPreview(c, theme)
		return
	}
	m := *item.ClipboardMedia
	cat := ClassifyClipboardMedia(m)
	snap := a.MediaProbe(m)
	path := mediaPrimaryPath(m)

	title := strings.TrimSpace(item.Title)
	if title == "" && path != "" {
		title = filepath.Base(path)
	}

	// Header + actions bar.
	ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
		kitIconTile(c, theme, MediaCategoryIcon(cat), true)
		ui.Column(c).Grow(1).MinWidth(space0).Gap(space1).Children(func() {
			ui.Text(c, title).FontSize(typeTitle).FontWeight(600).TextColor(theme.Text).SingleLine()
			ui.Text(c, MediaCategoryLabel(cat)+" · "+nonEmptyStr(snap.Mime, nonEmptyStr(strings.ToUpper(strings.TrimPrefix(snap.Extension, ".")), string(cat)))).
				FontSize(typeMeta).TextColor(theme.TextMuted).SingleLine()
		})
		if ui.Button(c, i18n.T("media.action.copy_path")).Key("media-copy-path").Padding(space2, space8).Shrink(0).Clicked() {
			a.copySelectedMediaItem(item)
		}
		if ui.Button(c, i18n.T("media.action.recheck")).Key("media-recheck").Padding(space2, space8).Shrink(0).Clicked() {
			a.RetryMediaProbe(m)
		}
	})

	// Metadata pill strip (real facts only).
	metaChips := buildMediaPreviewMetaChips(m, snap, item)
	if len(metaChips) > 0 {
		ui.Row(c).FillWidth().Wrap().Gap(space4).AlignItems(ui.Center).Children(func() {
			for _, chipText := range metaChips {
				kitStatusPill(c, theme, chipText, chipNeutral)
			}
		})
	}

	// Body: Loading / Unavailable / Category-specific real preview.
	kitCard(c, theme, cardInset, func() {
		ui.Column(c).FillWidth().Gap(space8).Children(func() {
			switch snap.Status {
			case MediaProbeLoading:
				ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Padding(space12, space4).Children(func() {
					kitStatusDot(c, kitStateLoadingColor(theme), true)
					ui.Text(c, i18n.T("media.preview.probing")).
						FontSize(typeLabel).TextColor(theme.TextMuted).Wrap().Grow(1)
				})

			case MediaProbeUnavailable:
				a.renderMediaUnavailablePreviewBody(c, theme, m, snap)

			default:
				a.renderMediaReadyPreviewBody(c, theme, m, snap)
			}
		})
	})

	// Real path readout at the bottom (never fabricated).
	if path != "" {
		ui.Column(c).FillWidth().Gap(space2).Children(func() {
			ui.Text(c, i18n.T("media.preview.local_path")).FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			ui.Text(c, path).FontSize(typeMeta).TextColor(theme.Text).Wrap()
		})
	}
}

func (a *AppController) renderMediaUnavailablePreviewBody(
	c *ui.Context,
	theme *ui.Theme,
	m engine.ClipboardMedia,
	snap MediaProbeSnapshot,
) {
	ui.Box(c).FillWidth().Padding(space10, space12).Radius(radiusRow).
		Background(theme.Warning.Alpha(alphaSubtle)).Border(space1, theme.Warning.Alpha(alphaHalf)).Children(func() {
		ui.Column(c).FillWidth().Gap(space6).Children(func() {
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				renderSemanticIcon(c, "exclamationmark.triangle", IconSize20, theme.Warning)
				ui.Text(c, i18n.T("media.unavailable.title")).
					FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).SingleLine().Grow(1)
				kitStatusPill(c, theme, i18n.T("media.unavailable.badge"), chipWarning)
			})
			reason := strings.TrimSpace(snap.UnavailableReason)
			if reason == "" {
				reason = i18n.T("preview.media.unavailable")
			}
			ui.Text(c, i18n.Tf("media.unavailable.reason", reason)).FontSize(typeMeta).TextColor(theme.Text).Wrap()
			ui.Text(c, i18n.T("media.unavailable.hint")).
				FontSize(typeMicro).TextColor(theme.TextMuted).Wrap()
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, i18n.T("media.unavailable.recheck_btn")).Key("media-unavailable-recheck").Padding(space2, space8).Clicked() {
					a.RetryMediaProbe(m)
				}
			})
		})
	})
}

func (a *AppController) renderMediaReadyPreviewBody(
	c *ui.Context,
	theme *ui.Theme,
	m engine.ClipboardMedia,
	snap MediaProbeSnapshot,
) {
	switch snap.Category {
	case MediaCategoryImage:
		a.renderClipboardMediaToolbar(c, theme, m)
		ui.Box(c).FillWidth().Height(mediaPreviewCanvasHeight(m)).Clip().Children(func() {
			a.renderClipboardMediaCanvas(c, theme, m)
		})

	case MediaCategoryVideo:
		if bmp := a.ThumbBitmap(m.ThumbPath); bmp != nil {
			ui.Image(c, bmp).FillWidth().Height(mediaCanvasMinHeight).Fit(ui.Contain).Radius(radiusChip)
		} else {
			renderMediaContainerSummaryBox(c, theme, "film",
				i18n.T("media.video.ready_title"),
				i18n.T("media.video.ready_hint"))
		}
		renderMediaKeyValueGrid(c, theme, []mediaKV{
			{i18n.T("media.meta.container"), nonEmptyStr(strings.ToUpper(strings.TrimPrefix(snap.Extension, ".")), "VIDEO")},
			{i18n.T("media.meta.duration"), formatDurationOptional(snap.DurationMs)},
			{i18n.T("media.meta.resolution"), formatDimensionsOptional(snap.Width, snap.Height)},
			{i18n.T("media.meta.size"), humanBytes(snap.ByteSize)},
		})

	case MediaCategoryAudio:
		renderMediaContainerSummaryBox(c, theme, "clock",
			i18n.T("media.audio.ready_title"),
			i18n.T("media.audio.ready_hint"))
		renderMediaKeyValueGrid(c, theme, []mediaKV{
			{i18n.T("media.meta.format"), nonEmptyStr(strings.ToUpper(strings.TrimPrefix(snap.Extension, ".")), "AUDIO")},
			{i18n.T("media.meta.mime"), nonEmptyStr(snap.Mime, "audio/*")},
			{i18n.T("media.meta.duration"), formatDurationOptional(snap.DurationMs)},
			{i18n.T("media.meta.size"), humanBytes(snap.ByteSize)},
		})

	case MediaCategoryDocument:
		if snap.ExcerptRedacted {
			kitWarningBanner(c, theme, i18n.T("media.doc.redacted_warning"))
		} else if len(snap.TextExcerptLines) > 0 {
			ui.Text(c, i18n.T("media.doc.excerpt_title")).
				FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			joined := strings.Join(snap.TextExcerptLines, "\n")
			ui.Box(c).FillWidth().Padding(space8, space10).Radius(radiusChip).
				Background(theme.Surface).Border(space1, hairline(theme)).Children(func() {
				if kind := detectPreviewContentKind(joined, snap.Extension); kind != previewKindPlain {
					src, _, _ := formatMarkdownPreviewSource(joined, kind, snap.Extension, defaultMarkdownMaxBytes, 64)
					ui.Column(c).FillWidth().Children(func() {
						a.renderClavisMarkdown(c, theme, src, false)
					})
					return
				}
				ui.Column(c).FillWidth().Gap(space2).Children(func() {
					for _, line := range snap.TextExcerptLines {
						display := line
						if display == "" {
							display = " "
						}
						ui.Text(c, display).FontSize(typeMeta).TextColor(theme.Text).SingleLine()
					}
				})
			})
		} else {
			renderMediaContainerSummaryBox(c, theme, "doc.text",
				i18n.T("media.doc.binary_title"),
				i18n.T("media.doc.binary_hint"))
		}
		renderMediaKeyValueGrid(c, theme, []mediaKV{
			{i18n.T("media.meta.doc_type"), nonEmptyStr(strings.ToUpper(strings.TrimPrefix(snap.Extension, ".")), "DOC")},
			{i18n.T("media.meta.size"), humanBytes(snap.ByteSize)},
		})

	case MediaCategoryArchive:
		if snap.ArchiveEntryCount > 0 {
			ui.Row(c).FillWidth().Gap(space8).AlignItems(ui.Center).Children(func() {
				renderSemanticIcon(c, "doc.on.doc", IconSize16, theme.Accent)
				ui.Text(c, i18n.Tf("media.archive.contents_header", snap.ArchiveEntryCount, humanBytes(snap.ArchiveTotalBytes))).
					FontSize(typeMeta).FontWeight(600).TextColor(theme.Text).Wrap().Grow(1)
			})
			ui.Box(c).FillWidth().Padding(space8, space10).Radius(radiusChip).
				Background(theme.Surface).Border(space1, hairline(theme)).Children(func() {
				ui.Column(c).FillWidth().Gap(space2).Children(func() {
					for _, name := range snap.ArchiveEntries {
						ui.Text(c, "• "+name).FontSize(typeMeta).TextColor(theme.Text).SingleLine()
					}
					if snap.ArchiveEntryCount > len(snap.ArchiveEntries) {
						ui.Text(c, i18n.Tf("media.archive.more_entries", snap.ArchiveEntryCount-len(snap.ArchiveEntries))).
							FontSize(typeMicro).TextColor(theme.TextMuted).SingleLine()
					}
				})
			})
		} else {
			renderMediaContainerSummaryBox(c, theme, "doc.on.doc",
				i18n.T("media.archive.ready_title"),
				i18n.T("media.archive.ready_hint"))
		}

	default:
		if snap.IsDir && len(snap.DirEntries) > 0 {
			ui.Text(c, i18n.T("media.dir.contents_title")).
				FontSize(typeMicro).FontWeight(600).TextColor(theme.TextMuted).SingleLine()
			ui.Box(c).FillWidth().Padding(space8, space10).Radius(radiusChip).
				Background(theme.Surface).Border(space1, hairline(theme)).Children(func() {
				ui.Column(c).FillWidth().Gap(space2).Children(func() {
					for _, child := range snap.DirEntries {
						ui.Text(c, "• "+child).FontSize(typeMeta).TextColor(theme.Text).SingleLine()
					}
				})
			})
		} else {
			renderMediaContainerSummaryBox(c, theme, "folder",
				i18n.T("media.file.ready_title"),
				i18n.T("media.file.ready_hint"))
		}
		renderMediaKeyValueGrid(c, theme, []mediaKV{
			{i18n.T("media.meta.extension"), nonEmptyStr(strings.ToUpper(strings.TrimPrefix(snap.Extension, ".")), i18n.T("media.meta.none"))},
			{i18n.T("media.meta.size"), humanBytes(snap.ByteSize)},
		})
	}
}

type mediaKV struct {
	key, val string
}

func renderMediaKeyValueGrid(c *ui.Context, theme *ui.Theme, pairs []mediaKV) {
	ui.Grid(c).Columns(2).GapX(space10).GapY(space4).FillWidth().Children(func() {
		for _, pair := range pairs {
			settingsKeyValuePair(c, theme, pair.key, pair.val)
		}
	})
}

func renderMediaContainerSummaryBox(c *ui.Context, theme *ui.Theme, icon, title, subtitle string) {
	ui.Box(c).FillWidth().Padding(space10, space12).Radius(radiusChip).
		Background(theme.Surface).Border(space1, hairline(theme)).Children(func() {
		ui.Row(c).FillWidth().Gap(space10).AlignItems(ui.Center).Children(func() {
			kitIconTile(c, theme, icon, false)
			ui.Column(c).Grow(1).MinWidth(space0).Gap(space2).Children(func() {
				ui.Text(c, title).FontSize(typeLabel).FontWeight(600).TextColor(theme.Text).Wrap()
				ui.Text(c, subtitle).FontSize(typeMeta).TextColor(theme.TextMuted).Wrap()
			})
		})
	})
}

func buildMediaPreviewMetaChips(m engine.ClipboardMedia, snap MediaProbeSnapshot, item engine.OmniboxItem) []string {
	var out []string
	if src := strings.TrimSpace(item.ClipboardSourceApp); src != "" {
		out = append(out, src)
	}
	if snap.Width > 0 && snap.Height > 0 {
		out = append(out, fmt.Sprintf("%d×%d", snap.Width, snap.Height))
	}
	if snap.ByteSize > 0 {
		out = append(out, humanBytes(snap.ByteSize))
	} else if m.ByteSize > 0 {
		out = append(out, humanBytes(m.ByteSize))
	}
	if snap.DurationMs > 0 {
		s := snap.DurationMs / 1000
		out = append(out, fmt.Sprintf("%d:%02d", s/60, s%60))
	}
	if !snap.ModTime.IsZero() {
		out = append(out, snap.ModTime.Format("2006-01-02 15:04"))
	}
	return out
}

func formatDurationOptional(ms int64) string {
	if ms <= 0 {
		return i18n.T("media.meta.unknown")
	}
	s := ms / 1000
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func formatDimensionsOptional(w, h int) string {
	if w <= 0 || h <= 0 {
		return i18n.T("media.meta.unknown")
	}
	return fmt.Sprintf("%d×%d", w, h)
}

func nonEmptyStr(val, fallback string) string {
	if strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return fallback
}
