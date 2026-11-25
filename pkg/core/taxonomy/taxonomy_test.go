package taxonomy

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTagAndTags(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"  #Code-Review  ", "code-review"},
		{"###Env:Work", "env:work"},
		{"  multi   space\tTag  ", "multi space tag"},
		{"中文标签", "中文标签"},
		{"PROJ:中文项目名称", "proj:中文项目名称"},
		{"", ""},
		{"   ###   ", ""},
		{strings.Repeat("a", 30), strings.Repeat("a", MaxTagRunes)},
		{strings.Repeat("汉", 30), strings.Repeat("汉", MaxTagRunes)},
	}
	for _, tc := range cases {
		if got := NormalizeTag(tc.in); got != tc.want {
			t.Errorf("NormalizeTag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	raw := []string{
		" #Go ", "go", "", "###", "ENV:Work", "中文", "t4", "t5", "t6", "t7", "t8", "t9", "t10",
	}
	got := NormalizeTags(raw)
	want := []string{"go", "env:work", "中文", "t4", "t5", "t6", "t7", "t8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeTags = %v, want %v", got, want)
	}
	if empty := NormalizeTags([]string{" ", "##"}); empty != nil {
		t.Fatalf("NormalizeTags(empty) = %v, want nil", empty)
	}
}

func TestParseAndFormatTagNamespace(t *testing.T) {
	cases := []struct {
		in     string
		wantNS string
		wantV  string
		wantOK bool
	}{
		{"env:Work", "env", "work", true},
		{"#PROJ:clavis", "proj", "clavis", true},
		{"type:prompt", "type", "prompt", true},
		{"env:", "", "env:", false},
		{"foo:bar", "", "foo:bar", false},
		{"plain", "", "plain", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		ns, v, ok := ParseTagNamespace(tc.in)
		if ns != tc.wantNS || v != tc.wantV || ok != tc.wantOK {
			t.Errorf("ParseTagNamespace(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, ns, v, ok, tc.wantNS, tc.wantV, tc.wantOK)
		}
	}

	if got := FormatNamespacedTag("ENV", "Work"); got != "env:work" {
		t.Fatalf("FormatNamespacedTag = %q, want env:work", got)
	}
	if got := FormatNamespacedTag("env", "   "); got != "" {
		t.Fatalf("FormatNamespacedTag empty val = %q, want empty", got)
	}
}

func TestValidateSafeMetadataTag(t *testing.T) {
	unsafe := []string{
		"sk-proj-1234567890abcdef",
		"#sk-live1234",
		"ghp_1234567890abcdef1234",
		"gho_abcd1234",
		"github_pat_11ABCDEF0123456789",
		"xoxb-1234-5678-abcd",
		"my-password-tag",
		"db_passwd",
		"secret",
		"api_token",
		"api_key=123",
		"-----BEGIN PRIVATE KEY-----",
		"prefix-longer-than-24-runes-sk-12345678",
		"9f86d081884c7d659a2feaa0c55ad015",
		"aB3dE6gH9jK2mN5pQ8rS",
		"env:aB3dE6gH9jK2mN5pQ8rS",
		"Bearer abcdef12345",
	}
	for _, tag := range unsafe {
		err := ValidateSafeMetadataTag(tag)
		if !errors.Is(err, ErrUnsafeMetadataTag) {
			t.Errorf("ValidateSafeMetadataTag(%q) = %v, want ErrUnsafeMetadataTag", tag, err)
		}
	}

	safe := []string{
		"",
		"   ",
		"disk-usage",
		"task-1",
		"code-review",
		"env:work",
		"env:production-east",
		"proj:clavis",
		"type:prompt",
		"中文标签",
		"release-2026-11-25",
	}
	for _, tag := range safe {
		if err := ValidateSafeMetadataTag(tag); err != nil {
			t.Errorf("ValidateSafeMetadataTag(%q) unexpectedly rejected: %v", tag, err)
		}
	}
}

func TestParseFilterQueryAndMatches(t *testing.T) {
	q := ParseFilterQuery("codex #Review #review -#WIP is:fav env:Work provider:Codex kind:session status:running rights:internal locale:zh-CN incomplete:")
	if q.IsEmpty() {
		t.Fatal("expected non-empty FilterQuery")
	}
	if q.FreeText != "codex incomplete:" {
		t.Fatalf("FreeText = %q, want %q", q.FreeText, "codex incomplete:")
	}
	if !reflect.DeepEqual(q.IncludeTags, []string{"review"}) {
		t.Fatalf("IncludeTags = %v, want [review]", q.IncludeTags)
	}
	if !reflect.DeepEqual(q.ExcludeTags, []string{"wip"}) {
		t.Fatalf("ExcludeTags = %v, want [wip]", q.ExcludeTags)
	}
	if !q.FavoriteOnly {
		t.Fatal("expected FavoriteOnly = true")
	}
	wantFacets := map[string]string{
		"provider": "codex",
		"kind":     "session",
		"status":   "running",
		"rights":   "internal",
		"env":      "work",
		"locale":   "zh-cn",
	}
	if !reflect.DeepEqual(q.Facets, wantFacets) {
		t.Fatalf("Facets = %+v, want %+v", q.Facets, wantFacets)
	}

	if !ParseFilterQuery("   ").IsEmpty() {
		t.Fatal("whitespace query should be empty")
	}
	if !ParseFilterQuery("is:favorite").FavoriteOnly {
		t.Fatal("is:favorite should set FavoriteOnly")
	}

	qSimple := ParseFilterQuery("codex #review -#wip is:fav provider:codex")
	if !qSimple.Matches("Codex Refactor", []string{"Review", "go"}, true, map[string]string{"provider": "Codex"}) {
		t.Fatal("expected matching entity to pass")
	}
	if qSimple.Matches("Codex Refactor", []string{"review", "wip"}, true, map[string]string{"provider": "codex"}) {
		t.Fatal("excluded tag should reject entity")
	}
	if qSimple.Matches("Codex Refactor", []string{"review"}, false, map[string]string{"provider": "codex"}) {
		t.Fatal("non-favorite should fail is:fav")
	}
	if qSimple.Matches("Codex Refactor", []string{"review"}, true, map[string]string{"provider": "claude"}) {
		t.Fatal("mismatched facet should reject entity")
	}
	if qSimple.Matches("Claude Refactor", []string{"review"}, true, map[string]string{"provider": "codex"}) {
		t.Fatal("missing free text should reject entity")
	}
}

func TestAttributeKinds(t *testing.T) {
	validKinds := []AttributeKind{
		AttrText, AttrMono, AttrStatus, AttrCount, AttrTime, AttrScope, AttrSecurity,
	}
	for _, k := range validKinds {
		if !k.IsValid() {
			t.Errorf("%q should be valid", k)
		}
	}
	if AttributeKind("unknown").IsValid() {
		t.Error("unknown kind should not be valid")
	}
	if !AttrMono.UsesMonoFont() || AttrText.UsesMonoFont() {
		t.Error("UsesMonoFont mismatch")
	}
	if !AttrCount.UsesTabularNums() || !AttrTime.UsesTabularNums() || AttrText.UsesTabularNums() {
		t.Error("UsesTabularNums mismatch")
	}
}
