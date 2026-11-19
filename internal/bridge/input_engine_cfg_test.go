package bridge

import (
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

// An unrelated config edit must not be seen as an [input_engine] change, or
// the reload would rebuild the lexicon and drop the in-flight composition.
func TestInputEngineConfigEqualSkipsUnchangedRebuild(t *testing.T) {
	base := config.DefaultInputEngineConfig()
	base.Enabled = true
	base.FuzzyPinyin = []string{"z=zh"}
	base.CustomDictPaths = []string{"/tmp/a.dict.yaml"}

	same := base
	same.FuzzyPinyin = []string{"z=zh"} // distinct slice, same content
	same.CustomDictPaths = []string{"/tmp/a.dict.yaml"}
	if !inputEngineConfigEqual(base, same) {
		t.Fatal("identical [input_engine] must compare equal (reload must not rebuild)")
	}

	cases := map[string]func(*config.InputEngineConfig){
		"scheme":      func(c *config.InputEngineConfig) { c.Scheme = "xiaohe" },
		"candidates":  func(c *config.InputEngineConfig) { c.CandidateCount = 9 },
		"fuzzy":       func(c *config.InputEngineConfig) { c.FuzzyPinyin = []string{"c=ch"} },
		"dicts":       func(c *config.InputEngineConfig) { c.CustomDictPaths = []string{"/tmp/b.dict.yaml"} },
		"learning":    func(c *config.InputEngineConfig) { c.UserLearning = !c.UserLearning },
		"enabled":     func(c *config.InputEngineConfig) { c.Enabled = !c.Enabled },
		"shifttoggle": func(c *config.InputEngineConfig) { c.ShiftToggleASCII = !c.ShiftToggleASCII },
		"mode":        func(c *config.InputEngineConfig) { c.Mode = "native_imk" },
		"imksocket":   func(c *config.InputEngineConfig) { c.IMKSocketPath = "/tmp/x.sock" },
	}
	for name, mutate := range cases {
		changed := base
		changed.FuzzyPinyin = []string{"z=zh"}
		changed.CustomDictPaths = []string{"/tmp/a.dict.yaml"}
		mutate(&changed)
		if inputEngineConfigEqual(base, changed) {
			t.Errorf("%s change not detected — reload would skip a needed rebuild", name)
		}
	}

	for name, mutate := range map[string]func(*config.InputEngineConfig){
		"mixed":    func(c *config.InputEngineConfig) { c.MixedEnglish = !c.MixedEnglish },
		"autopair": func(c *config.InputEngineConfig) { c.AutoPairPunctuation = !c.AutoPairPunctuation },
	} {
		unchanged := base
		mutate(&unchanged)
		if !inputEngineConfigEqual(base, unchanged) {
			t.Errorf("%s parse-compatibility field must not trigger a rebuild", name)
		}
	}

	// Empty vs nil slices are the same configuration.
	a := config.DefaultInputEngineConfig()
	b := config.DefaultInputEngineConfig()
	b.FuzzyPinyin = []string{}
	b.CustomDictPaths = nil
	if !inputEngineConfigEqual(a, b) {
		t.Error("nil vs empty slice must compare equal")
	}
}
