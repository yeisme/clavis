package engine

import (
	"fmt"
	"strings"

	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/i18n"
)

func makeCaptureActionItems() []OmniboxItem {
	annotateAct := config.CLIAction("clavis-capture", "annotate")
	ocrAct := config.CLIAction("clavis-capture", "ocr")
	return []OmniboxItem{
		{
			ID:           "action:clavis-capture:annotate",
			Title:        copyMatrixLabel("截图并标注", "Capture & Annotate Screenshot"),
			Subtitle:     copyMatrixLabel("选区截图并使用矩形、箭头、马赛克与文字标注", "Capture region and annotate with Rect, Arrow, Mosaic, Text"),
			Icon:         "photo",
			Keywords:     []string{"screenshot", "capture", "annotate", "截图", "标注", "马赛克"},
			Score:        950,
			Kind:         ItemLeaderAction,
			LeaderAction: &annotateAct,
		},
		{
			ID:           "action:clavis-capture:ocr",
			Title:        copyMatrixLabel("截图并 AI OCR 识字", "Capture & AI OCR"),
			Subtitle:     copyMatrixLabel("选区截图后直接发送至已配置 Vision 模型识字", "Capture region and send straight to Vision OCR"),
			Icon:         "sparkles",
			Keywords:     []string{"screenshot", "ocr", "capture", "截图", "识字"},
			Score:        940,
			Kind:         ItemLeaderAction,
			LeaderAction: &ocrAct,
		},
	}
}

func copyMatrixLabel(zh, en string) string {
	if i18n.IsChinese() {
		return zh
	}
	return en
}

func formatMarkdownFence(raw string) string {
	trimmed := strings.TrimRight(raw, "\r\n")
	fence := "```"
	if strings.Contains(trimmed, "```") {
		fence = "````"
	}
	return fence + "\n" + trimmed + "\n" + fence
}

func mediaPathsCopyText(m ClipboardMedia) string {
	if len(m.FilePaths) > 0 {
		return strings.Join(m.FilePaths, "\n")
	}
	return strings.TrimSpace(m.Path)
}

func mediaPrimaryPath(m ClipboardMedia) string {
	if len(m.FilePaths) > 0 && strings.TrimSpace(m.FilePaths[0]) != "" {
		return strings.TrimSpace(m.FilePaths[0])
	}
	return strings.TrimSpace(m.Path)
}

func formatMediaMetadataSafe(item OmniboxItem, m ClipboardMedia) string {
	lines := []string{
		fmt.Sprintf("kind: %s", m.Kind),
	}
	if m.Mime != "" {
		lines = append(lines, fmt.Sprintf("mime: %s", m.Mime))
	}
	if m.Width > 0 && m.Height > 0 {
		lines = append(lines, fmt.Sprintf("dimensions: %dx%d", m.Width, m.Height))
	}
	if m.ByteSize > 0 {
		lines = append(lines, fmt.Sprintf("bytes: %d", m.ByteSize))
	}
	if m.DurationMs > 0 {
		lines = append(lines, fmt.Sprintf("duration_ms: %d", m.DurationMs))
	}
	if p := mediaPrimaryPath(m); p != "" {
		lines = append(lines, fmt.Sprintf("path: %s", p))
	}
	lines = append(lines, fmt.Sprintf("pinned: %t", item.KeyHint == KeyHintPinned))
	return strings.Join(lines, "\n")
}
