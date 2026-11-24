package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/yeisme/clavis/pkg/core/toml"
)

// QuickJumpKind classifies a bookmark target for the unified `#` switcher.
type QuickJumpKind string

const (
	QuickJumpDir       QuickJumpKind = "dir"
	QuickJumpURL       QuickJumpKind = "url"
	QuickJumpWorkspace QuickJumpKind = "workspace"
	QuickJumpWorktree  QuickJumpKind = "worktree"
)

// QuickJumpBookmark defines one user-configured `#` quick-jump destination.
type QuickJumpBookmark struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Kind   QuickJumpKind `json:"kind"`
	Target string        `json:"target"`
	Tags   []string      `json:"tags,omitempty"`
	Hotkey string        `json:"hotkey,omitempty"`
}

// QuickJumpConfig configures bookmarks in `quickjump.toml`.
type QuickJumpConfig struct {
	Bookmarks []QuickJumpBookmark `json:"bookmarks"`
}

// DefaultQuickJumpConfig returns an empty QuickJumpConfig so fresh installs
// have zero noise until the user or project adds bookmarks.
func DefaultQuickJumpConfig() QuickJumpConfig {
	return QuickJumpConfig{}
}

// Encode serializes QuickJumpConfig to TOML.
func (c QuickJumpConfig) Encode() string {
	tbl := toml.NewTable()
	if len(c.Bookmarks) > 0 {
		arr := make([]toml.Value, len(c.Bookmarks))
		for i, b := range c.Bookmarks {
			bt := toml.NewTable()
			bt.Set("id", toml.StringVal(b.ID))
			bt.Set("name", toml.StringVal(b.Name))
			bt.Set("kind", toml.StringVal(string(b.Kind)))
			bt.Set("target", toml.StringVal(b.Target))
			if len(b.Tags) > 0 {
				tags := make([]toml.Value, len(b.Tags))
				for ti, tg := range b.Tags {
					tags[ti] = toml.StringVal(tg)
				}
				bt.Set("tags", toml.ArrayVal(tags))
			}
			if b.Hotkey != "" {
				bt.Set("hotkey", toml.StringVal(b.Hotkey))
			}
			arr[i] = toml.TableVal(bt)
		}
		tbl.Set("bookmarks", toml.ArrayVal(arr))
	}
	encoded := toml.Encode(tbl)
	if strings.TrimSpace(encoded) == "" {
		return "# Clavis quickjump bookmarks (# prefix in Capsule Omnibox)\n"
	}
	return encoded
}

// DecodeQuickJumpConfig decodes `quickjump.toml` content safely.
func DecodeQuickJumpConfig(src string) (QuickJumpConfig, error) {
	cfg := DefaultQuickJumpConfig()
	tbl, err := toml.Parse(src)
	if err != nil {
		return cfg, err
	}
	if tbl == nil {
		return cfg, nil
	}
	arr, ok, err := optionalTableArray(tbl, "bookmarks", "quickjump")
	if err != nil {
		return cfg, err
	}
	if !ok {
		return cfg, nil
	}
	bookmarks := make([]QuickJumpBookmark, 0, len(arr))
	for i, bt := range arr {
		p := fmt.Sprintf("quickjump.bookmarks[%d]", i)
		id, _, err := optionalString(bt, "id", p)
		if err != nil {
			return cfg, err
		}
		name, _, err := optionalString(bt, "name", p)
		if err != nil {
			return cfg, err
		}
		rawKind, _, err := optionalString(bt, "kind", p)
		if err != nil {
			return cfg, err
		}
		target, _, err := optionalString(bt, "target", p)
		if err != nil {
			return cfg, err
		}
		tags, _, err := optionalStringArray(bt, "tags", p)
		if err != nil {
			return cfg, err
		}
		hotkey, _, err := optionalString(bt, "hotkey", p)
		if err != nil {
			return cfg, err
		}
		kind := QuickJumpKind(strings.ToLower(strings.TrimSpace(rawKind)))
		if kind == "" {
			kind = QuickJumpDir
		}
		bookmarks = append(bookmarks, QuickJumpBookmark{
			ID:     strings.TrimSpace(id),
			Name:   strings.TrimSpace(name),
			Kind:   kind,
			Target: strings.TrimSpace(target),
			Tags:   tags,
			Hotkey: strings.TrimSpace(hotkey),
		})
	}
	cfg.Bookmarks = bookmarks
	return cfg, nil
}

// ValidateQuickJump validates bookmark entries in `quickjump.toml`.
func ValidateQuickJump(cfg QuickJumpConfig) []ValidationIssue {
	var issues []ValidationIssue
	file := string(FileQuickJump)
	seen := make(map[string]struct{}, len(cfg.Bookmarks))
	for i, b := range cfg.Bookmarks {
		p := fmt.Sprintf("bookmarks[%d]", i)
		if strings.TrimSpace(b.ID) == "" {
			issues = append(issues, ValidationIssue{File: file, KeyPath: p + ".id", Severity: SeverityError, Message: "bookmark id is required"})
		} else {
			if _, dup := seen[b.ID]; dup {
				issues = append(issues, ValidationIssue{File: file, KeyPath: p + ".id", Severity: SeverityError, Message: "duplicate bookmark id: " + b.ID})
			}
			seen[b.ID] = struct{}{}
		}
		if strings.TrimSpace(b.Name) == "" {
			issues = append(issues, ValidationIssue{File: file, KeyPath: p + ".name", Severity: SeverityError, Message: "bookmark name is required"})
		}
		if strings.TrimSpace(b.Target) == "" {
			issues = append(issues, ValidationIssue{File: file, KeyPath: p + ".target", Severity: SeverityError, Message: "bookmark target is required"})
		}
		switch b.Kind {
		case "", QuickJumpDir, QuickJumpWorkspace, QuickJumpWorktree:
		case QuickJumpURL:
			u, err := url.Parse(b.Target)
			scheme := ""
			if u != nil {
				scheme = strings.ToLower(u.Scheme)
			}
			if err != nil || (scheme != "http" && scheme != "https") {
				issues = append(issues, ValidationIssue{File: file, KeyPath: p + ".target", Severity: SeverityError, Message: "url bookmark must use http or https scheme"})
			}
		default:
			issues = append(issues, ValidationIssue{File: file, KeyPath: p + ".kind", Severity: SeverityError, Message: "unsupported bookmark kind: " + string(b.Kind)})
		}
	}
	return issues
}
