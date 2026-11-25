package config

import (
	"fmt"

	"github.com/yeisme/clavis/pkg/core/toml"
)

// SnippetItem defines one inline expansion snippet.
type SnippetItem struct {
	ID           string   `json:"id"`
	Trigger      string   `json:"trigger"`
	Content      string   `json:"content"`
	Description  string   `json:"description"`
	WordBoundary bool     `json:"wordBoundary"`
	AppBundleIDs []string `json:"appBundleIds"`
	ProcessNames []string `json:"processNames,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Favorite     bool     `json:"favorite,omitempty"`
}

func decodeSnippetItem(tbl *toml.Table, path string) (SnippetItem, error) {
	id, err := requiredString(tbl, "id", path)
	if err != nil {
		return SnippetItem{}, err
	}
	trigger, err := requiredString(tbl, "trigger", path)
	if err != nil {
		return SnippetItem{}, err
	}
	content, err := requiredString(tbl, "content", path)
	if err != nil {
		return SnippetItem{}, err
	}
	desc, _, err := optionalString(tbl, "description", path)
	if err != nil {
		return SnippetItem{}, err
	}
	wordBoundary := true
	if b, ok, err := optionalBool(tbl, "word_boundary", path); err != nil {
		return SnippetItem{}, err
	} else if ok {
		wordBoundary = b
	}
	bundleIDs, _, err := optionalStringArray(tbl, "app_bundle_ids", path)
	if err != nil {
		return SnippetItem{}, err
	}
	if bundleIDs == nil {
		bundleIDs = []string{}
	}
	processNames, _, err := optionalStringArray(tbl, "process_names", path)
	if err != nil {
		return SnippetItem{}, err
	}
	if processNames == nil {
		processNames = []string{}
	}
	tags, _, err := optionalStringArray(tbl, "tags", path)
	if err != nil {
		return SnippetItem{}, err
	}
	if len(tags) == 0 {
		tags = nil
	}
	favorite, _, err := optionalBool(tbl, "favorite", path)
	if err != nil {
		return SnippetItem{}, err
	}
	return SnippetItem{
		ID:           id,
		Trigger:      trigger,
		Content:      content,
		Description:  desc,
		WordBoundary: wordBoundary,
		AppBundleIDs: bundleIDs,
		ProcessNames: processNames,
		Tags:         tags,
		Favorite:     favorite,
	}, nil
}

func (s SnippetItem) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("id", toml.StringVal(s.ID))
	t.Set("trigger", toml.StringVal(s.Trigger))
	t.Set("content", toml.StringVal(s.Content))
	t.Set("description", toml.StringVal(s.Description))
	t.Set("word_boundary", toml.BoolVal(s.WordBoundary))
	t.Set("app_bundle_ids", stringsToTOMLArray(s.AppBundleIDs))
	if len(s.ProcessNames) > 0 {
		t.Set("process_names", stringsToTOMLArray(s.ProcessNames))
	}
	if len(s.Tags) > 0 {
		t.Set("tags", stringsToTOMLArray(s.Tags))
	}
	if s.Favorite {
		t.Set("favorite", toml.BoolVal(true))
	}
	return t
}

// SnippetsConfig represents snippets.toml.
type SnippetsConfig struct {
	Items []SnippetItem `json:"items"`
}

func DefaultSnippetsConfig() SnippetsConfig {
	return SnippetsConfig{
		Items: []SnippetItem{
			{
				ID:           "email-signoff",
				Trigger:      ";sig",
				Content:      "Best regards,",
				Description:  "Standard email sign-off",
				WordBoundary: true,
				AppBundleIDs: []string{},
				ProcessNames: []string{},
			},
			{
				ID:           "shrug",
				Trigger:      ";shrug",
				Content:      `¯\_(ツ)_/¯`,
				Description:  "Shrug emoticon",
				WordBoundary: true,
				AppBundleIDs: []string{},
				ProcessNames: []string{},
			},
		},
	}
}

func DecodeSnippetsConfig(input string) (SnippetsConfig, error) {
	tbl, err := toml.Parse(input)
	if err != nil {
		return SnippetsConfig{}, err
	}
	return SnippetsConfigFromTOML(tbl)
}

func SnippetsConfigFromTOML(tbl *toml.Table) (SnippetsConfig, error) {
	tables, _, err := optionalTableArray(tbl, "snippets", "")
	if err != nil {
		return SnippetsConfig{}, err
	}
	items := make([]SnippetItem, 0, len(tables))
	for i, itemTbl := range tables {
		item, err := decodeSnippetItem(itemTbl, fmt.Sprintf("snippets[%d]", i))
		if err != nil {
			return SnippetsConfig{}, err
		}
		items = append(items, item)
	}
	return SnippetsConfig{Items: items}, nil
}

func (c SnippetsConfig) Encode() string {
	root := toml.NewTable()
	vals := make([]toml.Value, len(c.Items))
	for i, item := range c.Items {
		vals[i] = toml.TableVal(item.toTOML())
	}
	root.Set("snippets", toml.ArrayVal(vals))
	return toml.Encode(root)
}
