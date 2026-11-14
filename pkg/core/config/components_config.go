package config

import (
	"fmt"
	"github.com/yeisme/clavis/pkg/core/toml"
	"os"
	"path/filepath"
	"sort"
)

type ComponentKind string

const (
	ComponentOmniboxSource ComponentKind = "omnibox_source"
	ComponentBentoWidget   ComponentKind = "bento_widget"
	ComponentScriptCommand ComponentKind = "script_command"
)

type BentoWidgetSpec struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Order   int    `json:"order"`
	Size    string `json:"size"`
}
type SourceModuleSpec struct {
	ID       string `json:"id"`
	Enabled  bool   `json:"enabled"`
	Prefix   string `json:"prefix"`
	Priority int    `json:"priority"`
}
type ScriptComponentSpec struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Subtitle  string   `json:"subtitle"`
	Trigger   string   `json:"trigger"`
	Icon      string   `json:"icon"`
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	Mode      string   `json:"mode"`
	TimeoutMs int      `json:"timeoutMs"`
	Enabled   bool     `json:"enabled"`
}
type ComponentsConfig struct {
	Widgets []BentoWidgetSpec     `json:"widgets"`
	Sources []SourceModuleSpec    `json:"sources"`
	Scripts []ScriptComponentSpec `json:"scripts"`
}

func DefaultComponentsConfig() ComponentsConfig {
	return ComponentsConfig{Widgets: []BentoWidgetSpec{{"ime_wpm", true, 1, "2x1"}, {"memory_health", true, 2, "1x1"}, {"pomodoro", true, 3, "1x1"}, {"scratchpad", true, 4, "2x2"}, {"quick_calc", true, 5, "1x1"}, {"pinned_clips", true, 6, "2x1"}}, Sources: []SourceModuleSpec{{"apps", true, "", 10}, {"leader", true, "", 20}, {"clipboard", true, "", 30}, {"snippets", true, ";", 40}, {"prompts", true, "/", 50}, {"ai", true, "", 60}, {"glossary", true, "", 70}, {"emoji", true, "", 80}, {"calculator", true, "=", 90}, {"port_killer", true, "", 100}, {"scripts", true, "", 110}}}
}
func decodeWidget(t *toml.Table, p string) (BentoWidgetSpec, error) {
	id, e := requiredString(t, "id", p)
	if e != nil {
		return BentoWidgetSpec{}, e
	}
	en := true
	if b, o, e := optionalBool(t, "enabled", p); e != nil {
		return BentoWidgetSpec{}, e
	} else if o {
		en = b
	}
	ord := 0
	if n, o, e := optionalInt(t, "order", p); e != nil {
		return BentoWidgetSpec{}, e
	} else if o {
		ord = n
	}
	sz, _, e := optionalString(t, "size", p)
	return BentoWidgetSpec{id, en, ord, sz}, e
}
func (w BentoWidgetSpec) toml() *toml.Table {
	t := toml.NewTable()
	t.Set("id", toml.StringVal(w.ID))
	t.Set("enabled", toml.BoolVal(w.Enabled))
	t.Set("order", toml.IntVal(int64(w.Order)))
	t.Set("size", toml.StringVal(w.Size))
	return t
}
func decodeSource(t *toml.Table, p string) (SourceModuleSpec, error) {
	id, e := requiredString(t, "id", p)
	if e != nil {
		return SourceModuleSpec{}, e
	}
	en := true
	if b, o, e := optionalBool(t, "enabled", p); e != nil {
		return SourceModuleSpec{}, e
	} else if o {
		en = b
	}
	pre, _, e := optionalString(t, "prefix", p)
	if e != nil {
		return SourceModuleSpec{}, e
	}
	pri := 0
	if n, o, e := optionalInt(t, "priority", p); e != nil {
		return SourceModuleSpec{}, e
	} else if o {
		pri = n
	}
	return SourceModuleSpec{id, en, pre, pri}, nil
}
func (s SourceModuleSpec) toml() *toml.Table {
	t := toml.NewTable()
	t.Set("id", toml.StringVal(s.ID))
	t.Set("enabled", toml.BoolVal(s.Enabled))
	t.Set("prefix", toml.StringVal(s.Prefix))
	t.Set("priority", toml.IntVal(int64(s.Priority)))
	return t
}
func decodeScript(t *toml.Table, p string) (ScriptComponentSpec, error) {
	id, e := requiredString(t, "id", p)
	if e != nil {
		return ScriptComponentSpec{}, e
	}
	title, _, e := optionalString(t, "title", p)
	if e != nil {
		return ScriptComponentSpec{}, e
	}
	sub, _, e := optionalString(t, "subtitle", p)
	if e != nil {
		return ScriptComponentSpec{}, e
	}
	tr, _, e := optionalString(t, "trigger", p)
	if e != nil {
		return ScriptComponentSpec{}, e
	}
	ic, _, e := optionalString(t, "icon", p)
	if e != nil {
		return ScriptComponentSpec{}, e
	}
	cmd, e := requiredString(t, "command", p)
	if e != nil {
		return ScriptComponentSpec{}, e
	}
	args, _, e := optionalStringArray(t, "args", p)
	if e != nil {
		return ScriptComponentSpec{}, e
	}
	mode := "silent"
	if x, o, e := optionalString(t, "mode", p); e != nil {
		return ScriptComponentSpec{}, e
	} else if o {
		mode = x
	}
	tout := 5000
	if n, o, e := optionalInt(t, "timeout_ms", p); e != nil {
		return ScriptComponentSpec{}, e
	} else if o {
		tout = n
	}
	en := true
	if b, o, e := optionalBool(t, "enabled", p); e != nil {
		return ScriptComponentSpec{}, e
	} else if o {
		en = b
	}
	return ScriptComponentSpec{id, title, sub, tr, ic, cmd, args, mode, tout, en}, nil
}
func (s ScriptComponentSpec) toml() *toml.Table {
	t := toml.NewTable()
	t.Set("id", toml.StringVal(s.ID))
	t.Set("title", toml.StringVal(s.Title))
	t.Set("subtitle", toml.StringVal(s.Subtitle))
	t.Set("trigger", toml.StringVal(s.Trigger))
	t.Set("icon", toml.StringVal(s.Icon))
	t.Set("command", toml.StringVal(s.Command))
	t.Set("args", stringsToTOMLArray(s.Args))
	t.Set("mode", toml.StringVal(s.Mode))
	t.Set("timeout_ms", toml.IntVal(int64(s.TimeoutMs)))
	t.Set("enabled", toml.BoolVal(s.Enabled))
	return t
}
func DecodeComponentsConfig(input string) (ComponentsConfig, error) {
	t, e := toml.Parse(input)
	if e != nil {
		return ComponentsConfig{}, e
	}
	return ComponentsConfigFromTOML(t)
}
func ComponentsConfigFromTOML(t *toml.Table) (ComponentsConfig, error) {
	c := ComponentsConfig{}
	ws, _, e := optionalTableArray(t, "widgets", "")
	if e != nil {
		return c, e
	}
	for i, x := range ws {
		v, e := decodeWidget(x, fmt.Sprintf("widgets[%d]", i))
		if e != nil {
			return c, e
		}
		c.Widgets = append(c.Widgets, v)
	}
	ss, _, e := optionalTableArray(t, "sources", "")
	if e != nil {
		return c, e
	}
	for i, x := range ss {
		v, e := decodeSource(x, fmt.Sprintf("sources[%d]", i))
		if e != nil {
			return c, e
		}
		c.Sources = append(c.Sources, v)
	}
	cs, _, e := optionalTableArray(t, "scripts", "")
	if e != nil {
		return c, e
	}
	for i, x := range cs {
		v, e := decodeScript(x, fmt.Sprintf("scripts[%d]", i))
		if e != nil {
			return c, e
		}
		c.Scripts = append(c.Scripts, v)
	}
	return c, nil
}
func (c ComponentsConfig) Encode() string {
	r := toml.NewTable()
	w := make([]toml.Value, len(c.Widgets))
	for i, x := range c.Widgets {
		w[i] = toml.TableVal(x.toml())
	}
	r.Set("widgets", toml.ArrayVal(w))
	s := make([]toml.Value, len(c.Sources))
	for i, x := range c.Sources {
		s[i] = toml.TableVal(x.toml())
	}
	r.Set("sources", toml.ArrayVal(s))
	z := make([]toml.Value, len(c.Scripts))
	for i, x := range c.Scripts {
		z[i] = toml.TableVal(x.toml())
	}
	r.Set("scripts", toml.ArrayVal(z))
	return toml.Encode(r)
}
func ValidateComponents(c ComponentsConfig) []ValidationIssue {
	var out []ValidationIssue
	seen := map[string]bool{}
	for i, w := range c.Widgets {
		p := fmt.Sprintf("widgets[%d]", i)
		if w.ID == "" {
			out = append(out, ValidationIssue{File: string(FileComponents), KeyPath: p + ".id", Severity: SeverityError, Message: "widget id cannot be empty"})
		}
		if seen[w.ID] {
			out = append(out, ValidationIssue{File: string(FileComponents), KeyPath: p + ".id", Severity: SeverityError, Message: "duplicate component id"})
		}
		seen[w.ID] = true
		if w.Size != "1x1" && w.Size != "2x1" && w.Size != "2x2" {
			out = append(out, ValidationIssue{File: string(FileComponents), KeyPath: p + ".size", Severity: SeverityError, Message: "invalid widget size"})
		}
	}
	for i, s := range c.Scripts {
		p := fmt.Sprintf("scripts[%d]", i)
		if s.ID == "" || s.Command == "" {
			out = append(out, ValidationIssue{File: string(FileComponents), KeyPath: p, Severity: SeverityError, Message: "script id and command are required"})
			if s.ID != "" {
				seen[s.ID] = true
			}
		}
	}
	return out
}
func mergeComponentScripts(c ComponentsConfig, dir string) (ComponentsConfig, error) {
	files, e := filepath.Glob(filepath.Join(dir, "components", "*.toml"))
	if e != nil {
		return c, e
	}
	sort.Strings(files)
	for _, f := range files {
		b, e := os.ReadFile(f)
		if e != nil {
			return c, e
		}
		x, e := DecodeComponentsConfig(string(b))
		if e != nil {
			return c, e
		}
		for _, script := range x.Scripts {
			found := false
			for i := range c.Scripts {
				if c.Scripts[i].ID == script.ID {
					c.Scripts[i] = script
					found = true
					break
				}
			}
			if !found {
				c.Scripts = append(c.Scripts, script)
			}
		}
	}
	return c, nil
}
