package uiapp

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// lockCheckingStore is an API key store that reports a read made while the
// controller's mutex is held. On the main thread MyGo reads the keychain in
// a nested event loop that redraws other windows, whose views take that
// mutex: a read under it hung Clavis at launch (Settings' first frame held
// it, the first-run guide's frame waited for it).
type lockCheckingStore struct {
	secrets.APIKeyStore
	t      *testing.T
	ctrl   atomic.Pointer[AppController]
	step   atomic.Value // string: what the test is doing
	checks atomic.Int32
}

// check waits a little for the controller's mutex: another goroutine may
// hold it for a moment, but a caller holding it never lets go while it waits
// for this read.
func (s *lockCheckingStore) check() {
	c := s.ctrl.Load()
	if c == nil {
		return
	}
	s.checks.Add(1)
	for i := 0; i < 200; i++ {
		if c.mu.TryLock() {
			c.mu.Unlock()
			return
		}
		time.Sleep(time.Millisecond)
	}
	s.t.Errorf("%v: the keychain was read with the controller locked", s.step.Load())
}

func (s *lockCheckingStore) ReadKey(account string) (string, error) {
	s.check()
	return s.APIKeyStore.ReadKey(account)
}

func (s *lockCheckingStore) ResolveAPIKeySourceWithEnv(account, envVar string) (secrets.APIKeySource, error) {
	s.check()
	return s.APIKeyStore.ResolveAPIKeySourceWithEnv(account, envVar)
}

// TestKeychainNeverReadUnderControllerLock drives every path that refreshes
// the API key source and requires each keychain read to happen unlocked,
// with the key source still current right after each change.
func TestKeychainNeverReadUnderControllerLock(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("CLAVIS_API_KEY", "")
	cfgStore := config.NewConfigStore(t.TempDir())
	if _, err := cfgStore.LoadOrInitialize(); err != nil {
		t.Fatal(err)
	}
	store := &lockCheckingStore{APIKeyStore: secrets.NewMemoryAPIKeyStore(), t: t}
	ctrl := NewAppController(Options{
		ConfigStore:    cfgStore,
		ClipboardStore: clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		SecretStore:    store,
		Bridge:         newMockBridge(),
	})
	t.Cleanup(ctrl.Close)
	store.ctrl.Store(ctrl)
	step := func(name string) { store.step.Store(name) }

	step("Settings frames")
	ctrl.setSettingsPage("ai")
	tt := ui.NewTester(ctrl.SettingsView, 960, 640)
	tt.Frame()
	tt.Frame()

	step("SaveAPIKey")
	if err := ctrl.SaveAPIKey("sk-lock-test"); err != nil {
		t.Fatal(err)
	}
	if got := ctrl.apiKeySourceNow(); got != secrets.KeySourceKeychain {
		t.Fatalf("after SaveAPIKey the key source is %v, want keychain", got)
	}

	step("SelectAIProfile")
	if err := ctrl.SelectAIProfile("ollama"); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.SelectAIProfile("deepseek"); err != nil {
		t.Fatal(err)
	}
	step("Dispatch EventSelectAIProfile")
	ctrl.Dispatch(engine.Event{Type: engine.EventSelectAIProfile, ProfileID: "deepseek"})
	step("UpdateMainConfig")
	if err := ctrl.UpdateMainConfig(func(m *config.MainConfig) { m.Appearance.ShowKeyHints = !m.Appearance.ShowKeyHints }); err != nil {
		t.Fatal(err)
	}
	step("ReloadConfig")
	ctrl.ReloadConfig()
	step("APIKeyStatus")
	ctrl.APIKeyStatus()

	step("DeleteAPIKey")
	if err := ctrl.DeleteAPIKey(); err != nil {
		t.Fatal(err)
	}
	if got := ctrl.apiKeySourceNow(); got != secrets.KeySourceNone {
		t.Fatalf("after DeleteAPIKey the key source is %v, want none", got)
	}

	step("OpenSettings")
	before := store.checks.Load()
	ctrl.OpenSettings(SettingsTabAI)
	waitUntil(t, 2*time.Second, func() bool { return store.checks.Load() > before }, "OpenSettings refreshes the key source")

	if store.checks.Load() < 8 {
		t.Fatalf("only %d keychain reads were checked; the test no longer reaches the refreshes", store.checks.Load())
	}
}

// apiKeySourceNow is the cached key source, as Settings shows it.
func (c *AppController) apiKeySourceNow() secrets.APIKeySource {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.apiKeySource
}
