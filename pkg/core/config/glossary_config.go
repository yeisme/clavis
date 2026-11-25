package config

import (
	"fmt"

	"github.com/yeisme/clavis/pkg/core/toml"
)

// GlossaryTerm defines a canonical domain term and its aliases.
type GlossaryTerm struct {
	Term     string   `json:"term"`
	Aliases  []string `json:"aliases"`
	Hint     string   `json:"hint"`
	Category string   `json:"category"`
	Tags     []string `json:"tags,omitempty"`
	Favorite bool     `json:"favorite,omitempty"`
}

func decodeGlossaryTerm(tbl *toml.Table, path string) (GlossaryTerm, error) {
	term, err := requiredString(tbl, "term", path)
	if err != nil {
		return GlossaryTerm{}, err
	}
	aliases, _, err := optionalStringArray(tbl, "aliases", path)
	if err != nil {
		return GlossaryTerm{}, err
	}
	if aliases == nil {
		aliases = []string{}
	}
	hint, _, err := optionalString(tbl, "hint", path)
	if err != nil {
		return GlossaryTerm{}, err
	}
	category := "general"
	if s, ok, err := optionalString(tbl, "category", path); err != nil {
		return GlossaryTerm{}, err
	} else if ok {
		category = s
	}
	tags, _, err := optionalStringArray(tbl, "tags", path)
	if err != nil {
		return GlossaryTerm{}, err
	}
	if len(tags) == 0 {
		tags = nil
	}
	favorite, _, err := optionalBool(tbl, "favorite", path)
	if err != nil {
		return GlossaryTerm{}, err
	}
	return GlossaryTerm{
		Term:     term,
		Aliases:  aliases,
		Hint:     hint,
		Category: category,
		Tags:     tags,
		Favorite: favorite,
	}, nil
}

func (g GlossaryTerm) toTOML() *toml.Table {
	t := toml.NewTable()
	t.Set("term", toml.StringVal(g.Term))
	t.Set("aliases", stringsToTOMLArray(g.Aliases))
	t.Set("hint", toml.StringVal(g.Hint))
	t.Set("category", toml.StringVal(g.Category))
	if len(g.Tags) > 0 {
		t.Set("tags", stringsToTOMLArray(g.Tags))
	}
	if g.Favorite {
		t.Set("favorite", toml.BoolVal(true))
	}
	return t
}

// GlossaryConfig represents glossary.toml.
type GlossaryConfig struct {
	Terms []GlossaryTerm `json:"terms"`
}

func DefaultGlossaryConfig() GlossaryConfig {
	return GlossaryConfig{
		Terms: []GlossaryTerm{
			{
				Term:     "Clavis",
				Aliases:  []string{"clavis"},
				Hint:     "Keep capitalized as the product name.",
				Category: "product",
			},
		},
	}
}

func DecodeGlossaryConfig(input string) (GlossaryConfig, error) {
	tbl, err := toml.Parse(input)
	if err != nil {
		return GlossaryConfig{}, err
	}
	return GlossaryConfigFromTOML(tbl)
}

func GlossaryConfigFromTOML(tbl *toml.Table) (GlossaryConfig, error) {
	tables, _, err := optionalTableArray(tbl, "terms", "")
	if err != nil {
		return GlossaryConfig{}, err
	}
	terms := make([]GlossaryTerm, 0, len(tables))
	for i, itemTbl := range tables {
		term, err := decodeGlossaryTerm(itemTbl, fmt.Sprintf("terms[%d]", i))
		if err != nil {
			return GlossaryConfig{}, err
		}
		terms = append(terms, term)
	}
	return GlossaryConfig{Terms: terms}, nil
}

func (c GlossaryConfig) Encode() string {
	root := toml.NewTable()
	vals := make([]toml.Value, len(c.Terms))
	for i, term := range c.Terms {
		vals[i] = toml.TableVal(term.toTOML())
	}
	root.Set("terms", toml.ArrayVal(vals))
	return toml.Encode(root)
}
