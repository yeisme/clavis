package engine

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/yeisme/clavis/pkg/core/config"
)

// QuickJumpTarget represents a live workspace, pane, directory, worktree, or URL destination.
type QuickJumpTarget struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Subtitle string  `json:"subtitle,omitempty"`
	Kind    string   `json:"kind"` // "workspace" | "agent" | "dir" | "worktree" | "url"
	Target  string   `json:"target"`
	Tags    []string `json:"tags,omitempty"`
}

func expandQuickJumpTemplate(target, queryRest string) string {
	trimmed := strings.TrimSpace(target)
	if strings.Contains(trimmed, "{query}") {
		q := url.QueryEscape(strings.TrimSpace(queryRest))
		return strings.ReplaceAll(trimmed, "{query}", q)
	}
	return trimmed
}

func expandHomeTilde(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if trimmed == "~" {
				return home
			}
			return filepath.Join(home, trimmed[2:])
		}
	}
	return trimmed
}

// BuildQuickJumpItems aggregates live Herdr workspaces/agents, configured quickjump.toml
// bookmarks (dirs, worktrees, URL templates), and listening ports under `#` / `＃`.
func BuildQuickJumpItems(state State, cfg config.ClavisConfig, rest string) []OmniboxItem {
	var candidates []OmniboxItem
	seen := make(map[string]struct{})

	// Split rest on whitespace for URL template parameterization when bookmark matches.
	// E.g. `#gh clavis` -> filter by "gh clavis" OR expand first token match + remainder.
	filterQuery := strings.TrimSpace(rest)

	// 1. Live Herdr workspaces & agent panes synced into State.QuickJumpTargets.
	for _, qt := range state.QuickJumpTargets {
		key := qt.Kind + ":" + qt.Target
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		icon := "terminal"
		switch qt.Kind {
		case "workspace":
			icon = "square.3.layers.3d"
		case "dir", "worktree":
			icon = "folder"
		case "url":
			icon = "link"
		}
		qj := qt
		kws := append([]string{"jump", qt.Kind, qt.Target, qt.Title}, qt.Tags...)
		candidates = append(candidates, OmniboxItem{
			ID:        "qj:" + qt.ID,
			Title:     qt.Title,
			Subtitle:  qt.Subtitle,
			KeyHint:   qt.Kind,
			Icon:      icon,
			Keywords:  kws,
			Kind:      ItemQuickJump,
			QuickJump: &qj,
			Score:     1050,
		})
	}

	// 2. Bookmarks from quickjump.toml.
	searchTerm := ""
	tokens := strings.Fields(filterQuery)
	if len(tokens) > 1 {
		searchTerm = strings.Join(tokens[1:], " ")
	}
	for _, b := range cfg.QuickJump.Bookmarks {
		resolvedTarget := b.Target
		effectiveFilter := filterQuery
		if b.Kind == config.QuickJumpURL && strings.Contains(b.Target, "{query}") && len(tokens) >= 1 {
			// Allow matching bookmark name/tags by first token while expanding trailing query.
			resolvedTarget = expandQuickJumpTemplate(b.Target, searchTerm)
			if len(tokens) > 1 {
				effectiveFilter = tokens[0]
			}
			_ = effectiveFilter
		}
		if b.Kind == config.QuickJumpDir || b.Kind == config.QuickJumpWorktree {
			resolvedTarget = expandHomeTilde(resolvedTarget)
		}
		key := string(b.Kind) + ":" + resolvedTarget
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		icon := "folder"
		switch b.Kind {
		case config.QuickJumpURL:
			icon = "link"
		case config.QuickJumpWorkspace:
			icon = "square.3.layers.3d"
		}
		qt := QuickJumpTarget{
			ID:       "bm:" + b.ID,
			Title:    b.Name,
			Subtitle: resolvedTarget,
			Kind:     string(b.Kind),
			Target:   resolvedTarget,
			Tags:     append([]string(nil), b.Tags...),
		}
		kws := append([]string{b.ID, b.Name, b.Target, string(b.Kind)}, b.Tags...)
		candidates = append(candidates, OmniboxItem{
			ID:        "qj:bm:" + b.ID,
			Title:     b.Name,
			Subtitle:  resolvedTarget,
			KeyHint:   b.Hotkey,
			Icon:      icon,
			Keywords:  kws,
			Kind:      ItemQuickJump,
			QuickJump: &qt,
			Score:     1020,
		})
	}

	// 3. Active Listening Ports.
	for _, port := range state.ListeningPorts {
		portStr := fmt.Sprintf("%d", port.Port)
		target := fmt.Sprintf("http://127.0.0.1:%d", port.Port)
		qt := QuickJumpTarget{
			ID:       "port:" + portStr,
			Title:    fmt.Sprintf("Port :%d · %s", port.Port, port.Process),
			Subtitle: port.Command,
			Kind:     "url",
			Target:   target,
		}
		candidates = append(candidates, OmniboxItem{
			ID:          "qj:port:" + portStr,
			Title:       qt.Title,
			Subtitle:    target + " · " + port.Command,
			Icon:        "bolt",
			Keywords:    []string{"port", portStr, port.Process},
			Kind:        ItemQuickJump,
			QuickJump:   &qt,
			Port:        port.Port,
			PortPID:     port.PID,
			PortProcess: port.Process,
			Score:       980,
		})
	}

	if filterQuery == "" {
		return candidates
	}
	// If URL template query has `<alias> <args>`, match on first token as fallback if full phrase misses.
	ranked := scoreCandidatesWithFrecency(candidates, filterQuery, state.FrecencyScores)
	if len(ranked) == 0 && len(tokens) > 1 {
		ranked = scoreCandidatesWithFrecency(candidates, tokens[0], state.FrecencyScores)
	}
	return ranked
}
