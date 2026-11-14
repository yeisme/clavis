package bridge

import "github.com/yeisme/clavis/pkg/core/clipboard"

// winClipFormats records which clipboard formats are present (no data is read to decide).
type winClipFormats struct {
	Text  bool // non-empty CF_UNICODETEXT
	HDrop bool // CF_HDROP file list
	PNG   bool // registered "PNG"
	DIB   bool // CF_DIB or CF_DIBV5
}

// classifyClipboardFormats applies docs/design/media-preview.md §5 priority: text wins (so
// Excel/Word copies that also carry a bitmap stay text), then files, then images.
func classifyClipboardFormats(f winClipFormats) clipboard.Kind {
	switch {
	case f.Text:
		return clipboard.KindText
	case f.HDrop:
		return clipboard.KindFile
	case f.PNG || f.DIB:
		return clipboard.KindImage
	}
	return ""
}
