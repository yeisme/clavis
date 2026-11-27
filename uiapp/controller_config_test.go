package uiapp

import (
	"sync"
	"testing"

	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/agentsession"
	"github.com/yeisme/clavis/pkg/core/clipboard"
	"github.com/yeisme/clavis/pkg/core/config"
)

func TestControllerSetConfigConcurrentWithQuickJumpSync(t *testing.T) {
	store := config.NewConfigStore(t.TempDir())
	if _, err := store.LoadOrInitialize(); err != nil {
		t.Fatalf("LoadOrInitialize: %v", err)
	}
	ctrl := NewAppController(Options{
		ConfigStore:     store,
		ClipboardStore:  clipboard.NewMemoryStore(clipboard.NewPrivacyFilter()),
		SecretStore:     secrets.NewMemoryAPIKeyStore(),
		ClipboardWriter: func(string) {},
	})
	defer ctrl.Close()

	baseCfg := store.Current()

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := range 50 {
			cfg := baseCfg
			if i%2 == 0 {
				cfg.Snippets.Items = []config.SnippetItem{{
					ID:      "snip-a",
					Trigger: ";a",
					Content: "alpha",
				}}
			} else {
				cfg.Workflows.Workflows = []config.Workflow{{
					ID:   "wf-a",
					Name: "Workflow A",
				}}
			}
			ctrl.SetConfig(cfg)
		}
	}()

	go func() {
		defer wg.Done()
		for i := range 50 {
			ws := []agentsession.HerdrWorkspace{{
				WorkspaceID: "ws-1",
				Label:       "Main",
				AgentStatus: "active",
			}}
			if i%2 == 1 {
				ws = append(ws, agentsession.HerdrWorkspace{
					WorkspaceID: "ws-2",
					Label:       "Side",
					AgentStatus: "idle",
				})
			}
			ctrl.syncAgentQuickJumpTargets(ws, nil)
		}
	}()

	go func() {
		defer wg.Done()
		for range 50 {
			_ = ctrl.State()
		}
	}()

	wg.Wait()
}
