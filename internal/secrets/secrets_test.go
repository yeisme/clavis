package secrets

import (
	"errors"
	"testing"

	"github.com/yeisme/clavis/pkg/core/config"
)

func TestDefaultEnvVarForProvider(t *testing.T) {
	cases := []struct {
		provider string
		want     string
	}{
		{"deepseek", "DEEPSEEK_API_KEY"},
		{"DeepSeek", "DEEPSEEK_API_KEY"},
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"claude", "ANTHROPIC_API_KEY"},
		{"gemini", "GEMINI_API_KEY"},
		{"google", "GEMINI_API_KEY"},
		{"ollama", ""},
		{"OLLAMA", ""},
		{"custom_provider", "CLAVIS_API_KEY"},
		{"", "CLAVIS_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
	}
	for _, tc := range cases {
		if got := DefaultEnvVarForProvider(tc.provider); got != tc.want {
			t.Errorf("DefaultEnvVarForProvider(%q) = %q, want %q", tc.provider, got, tc.want)
		}
	}
}

func TestMemoryAPIKeyStore_KeychainAndEnvFallback(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("CLAVIS_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	store := NewMemoryAPIKeyStore()
	account := "clavis.ai.deepseek"

	// Missing both keychain and env
	if _, err := store.ReadKey(account); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got %v", err)
	}
	if _, err := store.ResolveAPIKey(account); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got %v", err)
	}
	src, err := store.ResolveAPIKeySource(account)
	if err != nil || src != KeySourceNone {
		t.Fatalf("expected KeySourceNone, got src=%q err=%v", src, err)
	}

	// Env fallback via DEEPSEEK_API_KEY
	t.Setenv("DEEPSEEK_API_KEY", "  sk-env-12345  ")
	key, err := store.ResolveAPIKey(account)
	if err != nil || key != "sk-env-12345" {
		t.Fatalf("expected env key 'sk-env-12345', got %q (err=%v)", key, err)
	}
	src, err = store.ResolveAPIKeySource(account)
	if err != nil || src != KeySourceEnv {
		t.Fatalf("expected KeySourceEnv, got src=%q err=%v", src, err)
	}

	// Keychain overrides env
	if err := store.SaveKey(account, " sk-keychain-999 "); err != nil {
		t.Fatalf("SaveKey failed: %v", err)
	}
	key, err = store.ResolveAPIKey(account)
	if err != nil || key != "sk-keychain-999" {
		t.Fatalf("expected keychain key 'sk-keychain-999', got %q (err=%v)", key, err)
	}
	src, err = store.ResolveAPIKeySource(account)
	if err != nil || src != KeySourceKeychain {
		t.Fatalf("expected KeySourceKeychain, got src=%q err=%v", src, err)
	}

	// Delete falls back to env again
	if err := store.DeleteKey(account); err != nil {
		t.Fatalf("DeleteKey failed: %v", err)
	}
	if _, err := store.ReadKey(account); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound after delete, got %v", err)
	}
	key, err = store.ResolveAPIKey(account)
	if err != nil || key != "sk-env-12345" {
		t.Fatalf("expected fallback to env key after delete, got %q (err=%v)", key, err)
	}
}

func TestResolveAPIKeyWithEnv_AndMultiProfileHelpers(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-ds-env")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-env")
	t.Setenv("GEMINI_API_KEY", "")

	store := NewMemoryAPIKeyStore()

	// ResolveAPIKeyWithEnv uses explicit envVar
	antKey, err := store.ResolveAPIKeyWithEnv("clavis.ai.anthropic", "ANTHROPIC_API_KEY")
	if err != nil || antKey != "sk-ant-env" {
		t.Fatalf("ResolveAPIKeyWithEnv anthropic = (%q, %v), want sk-ant-env", antKey, err)
	}
	antSrc, err := store.ResolveAPIKeySourceWithEnv("clavis.ai.anthropic", "ANTHROPIC_API_KEY")
	if err != nil || antSrc != KeySourceEnv {
		t.Fatalf("ResolveAPIKeySourceWithEnv anthropic = (%q, %v), want KeySourceEnv", antSrc, err)
	}

	// Multi-profile resolution via ResolveProfileAPIKey / ResolveProfileAPIKeySource
	profiles := config.DefaultAIProfiles()
	var deepseekProf, ollamaProf, claudeProf, geminiProf config.AIProfile
	for _, p := range profiles {
		switch p.ID {
		case "deepseek":
			deepseekProf = p
		case "ollama":
			ollamaProf = p
		case "claude":
			claudeProf = p
		case "gemini":
			geminiProf = p
		}
	}

	// 1. DeepSeek from env
	k, err := ResolveProfileAPIKey(store, deepseekProf)
	if err != nil || k != "sk-ds-env" {
		t.Fatalf("ResolveProfileAPIKey(deepseek) = (%q, %v), want sk-ds-env", k, err)
	}
	src, err := ResolveProfileAPIKeySource(store, deepseekProf)
	if err != nil || src != KeySourceEnv {
		t.Fatalf("ResolveProfileAPIKeySource(deepseek) = (%q, %v), want env", src, err)
	}

	// 2. Claude keychain overrides env
	if err := store.SaveKey(claudeProf.KeychainAccount, "sk-ant-keychain"); err != nil {
		t.Fatal(err)
	}
	k, err = ResolveProfileAPIKey(store, claudeProf)
	if err != nil || k != "sk-ant-keychain" {
		t.Fatalf("ResolveProfileAPIKey(claude) = (%q, %v), want sk-ant-keychain", k, err)
	}
	src, err = ResolveProfileAPIKeySource(store, claudeProf)
	if err != nil || src != KeySourceKeychain {
		t.Fatalf("ResolveProfileAPIKeySource(claude) = (%q, %v), want keychain", src, err)
	}

	// 3. Gemini missing both keychain and GEMINI_API_KEY -> ErrSecretNotFound / KeySourceNone
	if _, err := ResolveProfileAPIKey(store, geminiProf); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound for unconfigured gemini, got %v", err)
	}
	src, err = ResolveProfileAPIKeySource(store, geminiProf)
	if err != nil || src != KeySourceNone {
		t.Fatalf("expected KeySourceNone for unconfigured gemini, got (%q, %v)", src, err)
	}

	// 4. Local Ollama keyless allowed -> returns ("", nil) and (KeySourceNone, nil)
	k, err = ResolveProfileAPIKey(store, ollamaProf)
	if err != nil || k != "" {
		t.Fatalf("expected keyless ('', nil) for local ollama, got (%q, %v)", k, err)
	}
	src, err = ResolveProfileAPIKeySource(store, ollamaProf)
	if err != nil || src != KeySourceNone {
		t.Fatalf("expected (KeySourceNone, nil) for local ollama, got (%q, %v)", src, err)
	}

	// 5. Local Ollama with optional stored key returns stored key
	if err := store.SaveKey(ollamaProf.KeychainAccount, "ollama-proxy-token"); err != nil {
		t.Fatal(err)
	}
	k, err = ResolveProfileAPIKey(store, ollamaProf)
	if err != nil || k != "ollama-proxy-token" {
		t.Fatalf("expected stored ollama key, got (%q, %v)", k, err)
	}
	src, err = ResolveProfileAPIKeySource(store, ollamaProf)
	if err != nil || src != KeySourceKeychain {
		t.Fatalf("expected KeySourceKeychain for stored ollama key, got (%q, %v)", src, err)
	}
}

func TestAPIKeyStore_Validation(t *testing.T) {
	store := NewMemoryAPIKeyStore()
	if err := store.SaveKey("", "sk-abc"); err == nil {
		t.Fatal("expected error for empty account")
	}
	if err := store.SaveKey("acct", "   "); err == nil {
		t.Fatal("expected error for empty API key")
	}
	if _, err := store.ReadKey("   "); err == nil {
		t.Fatal("expected error for empty account on ReadKey")
	}
	if err := store.DeleteKey(""); err == nil {
		t.Fatal("expected error for empty account on DeleteKey")
	}
}

func TestMyGoSecretsStore_EnvFallback(t *testing.T) {
	// Verify MyGoSecretsStore works safely before App.Run and resolves env fallback
	// when a unique non-existent keychain account is queried.
	t.Setenv("DEEPSEEK_API_KEY", "sk-mygo-env-fallback")
	t.Setenv("GEMINI_API_KEY", "sk-gemini-env")
	s := NewMyGoSecretsStore("com.yeisme.clavis.test.nonexistent")
	key, err := s.ResolveAPIKey("nonexistent.account.for.unit.test")
	if err != nil {
		t.Fatalf("ResolveAPIKey failed: %v", err)
	}
	if key != "sk-mygo-env-fallback" {
		t.Fatalf("expected sk-mygo-env-fallback, got %q", key)
	}
	src, err := s.ResolveAPIKeySource("nonexistent.account.for.unit.test")
	if err != nil {
		t.Fatalf("ResolveAPIKeySource failed: %v", err)
	}
	if src != KeySourceEnv {
		t.Fatalf("expected KeySourceEnv, got %q", src)
	}

	gKey, err := s.ResolveAPIKeyWithEnv("nonexistent.gemini.account", "GEMINI_API_KEY")
	if err != nil || gKey != "sk-gemini-env" {
		t.Fatalf("ResolveAPIKeyWithEnv failed: key=%q err=%v", gKey, err)
	}
	gSrc, err := s.ResolveAPIKeySourceWithEnv("nonexistent.gemini.account", "GEMINI_API_KEY")
	if err != nil || gSrc != KeySourceEnv {
		t.Fatalf("ResolveAPIKeySourceWithEnv failed: src=%q err=%v", gSrc, err)
	}
}
