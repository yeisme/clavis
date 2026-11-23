package config

import "testing"

func TestValidateKeyBindings(t *testing.T) {
	cases := []struct {
		name     string
		bindings []KeyBinding
		wantErr  bool
	}{
		{"valid chord", []KeyBinding{{Keys: []string{"ctrl+option+return"}, Action: "ai_continue"}}, false},
		{"unknown action", []KeyBinding{{Keys: []string{"ctrl+option+x"}, Action: "ai_nope"}}, true},
		{"bare key rejected", []KeyBinding{{Keys: []string{"return"}, Action: "ai_continue"}}, true},
		{"shift only rejected", []KeyBinding{{Keys: []string{"shift+return"}, Action: "ai_continue"}}, true},
		{"conflict", []KeyBinding{
			{Keys: []string{"ctrl+option+r"}, Action: "ai_continue"},
			{Keys: []string{"ctrl+option+r"}, Action: "ai_optimize"},
		}, true},
		{"same chord same action ok", []KeyBinding{
			{Keys: []string{"ctrl+option+r"}, Action: "ai_continue"},
			{Keys: []string{"ctrl+option+r"}, Action: "ai_continue"},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateKeyBindings(tc.bindings)
			if (len(errs) > 0) != tc.wantErr {
				t.Fatalf("errs=%v wantErr=%v", errs, tc.wantErr)
			}
		})
	}
}
