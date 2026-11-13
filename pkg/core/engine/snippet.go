package engine

import (
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/config"
)

// SnippetMatch describes a matched snippet expansion and cursor placement.
type SnippetMatch struct {
	Snippet          config.SnippetItem `json:"snippet"`
	RenderedText     string             `json:"renderedText"`
	CursorBackOffset int                `json:"cursorBackOffset"`
	DeleteCount      int                `json:"deleteCount"`
}

// SnippetRenderResult holds rendered snippet output and cursor metrics.
type SnippetRenderResult struct {
	RenderedText     string `json:"renderedText"`
	CursorBackOffset int    `json:"cursorBackOffset"`
	DeleteCount      int    `json:"deleteCount"`
}

// RenderOptions configures placeholder expansion for SnippetRenderer.
type RenderOptions struct {
	TriggerLength int
	Now           time.Time
	ClipboardText string
	AppName       string
	Location      *time.Location
}

// FeedOptions provides runtime context for SnippetEngine.Feed.
type FeedOptions struct {
	BundleID      string
	ProcessName   string
	Now           time.Time
	ClipboardText string
	AppName       string
	Location      *time.Location
}

// RenderSnippet expands {{date}}, {{date:FORMAT}}, {{time}}, {{time:FORMAT}},
// {{datetime}}, {{clipboard}}, {{app}}, and {{cursor}} without recursive expansion.
func RenderSnippet(template string, opts RenderOptions) SnippetRenderResult {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	loc := opts.Location
	if loc == nil {
		loc = time.Local
	}
	nowInLoc := now.In(loc)

	var out strings.Builder
	out.Grow(len(template))
	cursorRuneIdx := -1
	pos := 0

	for pos < len(template) {
		relOpen := strings.Index(template[pos:], "{{")
		if relOpen < 0 {
			out.WriteString(template[pos:])
			break
		}
		openIdx := pos + relOpen
		if openIdx > pos {
			out.WriteString(template[pos:openIdx])
		}
		searchFrom := openIdx + 2
		relClose := strings.Index(template[searchFrom:], "}}")
		if relClose < 0 {
			out.WriteString(template[openIdx:])
			break
		}
		closeIdx := searchFrom + relClose
		token := template[searchFrom:closeIdx]

		if token == "cursor" {
			if cursorRuneIdx == -1 {
				cursorRuneIdx = utf8.RuneCountInString(out.String())
			}
		} else if rep, ok := resolveSnippetToken(token, nowInLoc, opts.ClipboardText, opts.AppName); ok {
			out.WriteString(rep)
		} else {
			out.WriteString(template[openIdx : closeIdx+2])
		}
		pos = closeIdx + 2
	}

	rendered := out.String()
	totalRunes := utf8.RuneCountInString(rendered)
	backOffset := 0
	if cursorRuneIdx >= 0 && cursorRuneIdx <= totalRunes {
		backOffset = totalRunes - cursorRuneIdx
	}
	delCount := opts.TriggerLength
	if delCount < 0 {
		delCount = 0
	}

	return SnippetRenderResult{
		RenderedText:     rendered,
		CursorBackOffset: backOffset,
		DeleteCount:      delCount,
	}
}

func resolveSnippetToken(token string, now time.Time, clipboardText, appName string) (string, bool) {
	switch token {
	case "date":
		return now.Format("2006-01-02"), true
	case "time":
		return now.Format("15:04"), true
	case "datetime":
		return now.Format("2006-01-02 15:04"), true
	case "clipboard":
		return clipboardText, true
	case "app":
		return appName, true
	}
	if strings.HasPrefix(token, "date:") {
		rawFmt := strings.TrimPrefix(token, "date:")
		if rawFmt == "" {
			return now.Format("2006-01-02"), true
		}
		return now.Format(convertDateFormat(rawFmt)), true
	}
	if strings.HasPrefix(token, "time:") {
		rawFmt := strings.TrimPrefix(token, "time:")
		if rawFmt == "" {
			return now.Format("15:04"), true
		}
		return now.Format(convertDateFormat(rawFmt)), true
	}
	return "", false
}

// convertDateFormat translates common UTS-35 date/time format tokens (like yyyy-MM-dd HH:mm:ss)
// into Go time layout format strings while preserving already-Go layouts if no tokens match.
func convertDateFormat(fmtStr string) string {
	replacer := strings.NewReplacer(
		"yyyy", "2006",
		"yy", "06",
		"MMMM", "January",
		"MMM", "Jan",
		"MM", "01",
		"dd", "02",
		"HH", "15",
		"hh", "03",
		"mm", "04",
		"ss", "05",
		"EEEE", "Monday",
		"EEE", "Mon",
		"zzz", "MST",
	)
	return replacer.Replace(fmtStr)
}

type compiledSnippet struct {
	item         config.SnippetItem
	triggerRunes []rune
}

type runeRingBuffer struct {
	capacity   int
	storage    []rune
	writeIndex int
	count      int
}

func newRuneRingBuffer(capacity int) runeRingBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return runeRingBuffer{
		capacity: capacity,
		storage:  make([]rune, capacity),
	}
}

func (b *runeRingBuffer) push(r rune) {
	b.storage[b.writeIndex] = r
	b.writeIndex = (b.writeIndex + 1) % b.capacity
	if b.count < b.capacity {
		b.count++
	}
}

func (b *runeRingBuffer) popLast() {
	if b.count == 0 {
		return
	}
	b.writeIndex = (b.writeIndex - 1 + b.capacity) % b.capacity
	b.storage[b.writeIndex] = 0
	b.count--
}

func (b *runeRingBuffer) clear() {
	for i := range b.storage {
		b.storage[i] = 0
	}
	b.writeIndex = 0
	b.count = 0
}

func (b *runeRingBuffer) runeAtOffsetFromEnd(offset int) (rune, bool) {
	if offset < 0 || offset >= b.count {
		return 0, false
	}
	idx := (b.writeIndex - 1 - offset + b.capacity) % b.capacity
	return b.storage[idx], true
}

func (b *runeRingBuffer) asString() string {
	if b.count == 0 {
		return ""
	}
	var sb strings.Builder
	for offset := b.count - 1; offset >= 0; offset-- {
		if r, ok := b.runeAtOffsetFromEnd(offset); ok {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// SnippetEngine tracks typed characters in a rune ring buffer and matches snippet triggers.
type SnippetEngine struct {
	capacity int
	buffer   runeRingBuffer
	compiled []compiledSnippet
}

// NewSnippetEngine creates a SnippetEngine with the specified ring buffer capacity (default 64 if <= 0).
func NewSnippetEngine(snippets []config.SnippetItem, capacity int) *SnippetEngine {
	if capacity <= 0 {
		capacity = 64
	}
	e := &SnippetEngine{
		capacity: capacity,
		buffer:   newRuneRingBuffer(capacity),
	}
	e.UpdateSnippets(snippets)
	return e
}

// NewSnippetEngineFromConfig creates a SnippetEngine from SnippetsConfig with capacity 64.
func NewSnippetEngineFromConfig(cfg config.SnippetsConfig) *SnippetEngine {
	return NewSnippetEngine(cfg.Items, 64)
}

// UpdateSnippets recompiles the snippet list sorted longest-trigger-first.
func (e *SnippetEngine) UpdateSnippets(snippets []config.SnippetItem) {
	type indexed struct {
		idx   int
		item  config.SnippetItem
		runes []rune
	}
	var list []indexed
	for i, s := range snippets {
		if s.Trigger == "" {
			continue
		}
		list = append(list, indexed{
			idx:   i,
			item:  s,
			runes: []rune(s.Trigger),
		})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if len(list[i].runes) != len(list[j].runes) {
			return len(list[i].runes) > len(list[j].runes)
		}
		return list[i].idx < list[j].idx
	})
	e.compiled = make([]compiledSnippet, len(list))
	for i, entry := range list {
		e.compiled[i] = compiledSnippet{
			item:         entry.item,
			triggerRunes: entry.runes,
		}
	}
}

// CurrentBufferString returns the current ring buffer contents as a string.
func (e *SnippetEngine) CurrentBufferString() string {
	return e.buffer.asString()
}

// Feed pushes a typed rune into the ring buffer and checks for a snippet trigger match.
func (e *SnippetEngine) Feed(r rune, opts FeedOptions) (*SnippetMatch, bool) {
	e.buffer.push(r)

	for _, entry := range e.compiled {
		tlen := len(entry.triggerRunes)
		if tlen > e.buffer.count {
			continue
		}

		if !snippetMatchesApp(entry.item, opts.BundleID, opts.ProcessName) {
			continue
		}

		isMatch := true
		for i := 0; i < tlen; i++ {
			offsetFromEnd := tlen - 1 - i
			bufRune, ok := e.buffer.runeAtOffsetFromEnd(offsetFromEnd)
			if !ok || bufRune != entry.triggerRunes[i] {
				isMatch = false
				break
			}
		}
		if !isMatch {
			continue
		}

		if entry.item.WordBoundary {
			if preceding, ok := e.buffer.runeAtOffsetFromEnd(tlen); ok {
				if isSnippetWordRune(preceding) {
					continue
				}
			}
		}

		rendered := RenderSnippet(entry.item.Content, RenderOptions{
			TriggerLength: tlen,
			Now:           opts.Now,
			ClipboardText: opts.ClipboardText,
			AppName:       opts.AppName,
			Location:      opts.Location,
		})

		e.buffer.clear()

		return &SnippetMatch{
			Snippet:          entry.item,
			RenderedText:     rendered.RenderedText,
			CursorBackOffset: rendered.CursorBackOffset,
			DeleteCount:      rendered.DeleteCount,
		}, true
	}

	return nil, false
}

// Backspace removes the most recently pushed rune from the buffer.
func (e *SnippetEngine) Backspace() {
	e.buffer.popLast()
}

// Reset clears the ring buffer.
func (e *SnippetEngine) Reset() {
	e.buffer.clear()
}

func snippetMatchesApp(item config.SnippetItem, bundleID, processName string) bool {
	hasBundleFilter := len(item.AppBundleIDs) > 0
	hasProcessFilter := len(item.ProcessNames) > 0
	if !hasBundleFilter && !hasProcessFilter {
		return true
	}
	if hasBundleFilter && bundleID != "" {
		for _, id := range item.AppBundleIDs {
			if id == bundleID {
				return true
			}
		}
	}
	if hasProcessFilter && processName != "" {
		for _, pn := range item.ProcessNames {
			if strings.EqualFold(pn, processName) {
				return true
			}
		}
	}
	return false
}

func isSnippetWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
