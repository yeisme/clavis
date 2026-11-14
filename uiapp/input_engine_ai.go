package uiapp

import (
	"context"
	"strings"

	"github.com/yeisme/clavis/internal/secrets"
	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/inputengine"
)

// SuggestInputCandidates asks the active AI profile for pinyin candidates (design §5).
// Silent degrade: no client, no key, timeout or garbage → nil, no HUD, no diagnostics.
func (c *AppController) SuggestInputCandidates(ctx context.Context, raw, appName string) []string {
	if !inputengine.ShouldQueryAI(raw) {
		return nil
	}
	cfg := c.Config() // ConfigStore read; does not take c.mu
	c.mu.Lock()
	if c.AIClient == nil {
		c.mu.Unlock()
		return nil
	}
	client := *c.AIClient
	profile := c.activeAIProfileLocked()
	secStore := c.SecretStore
	c.mu.Unlock()

	client.Config = cfg.Main.AI
	client.APIKeyProvider = func(context.Context) (string, error) {
		return secrets.ResolveProfileAPIKey(secStore, profile)
	}
	ctx, cancel := context.WithTimeout(ctx, inputengine.AITimeout)
	defer cancel()
	var reply strings.Builder
	err := client.StreamCompletion(ctx, ai.CompletionRequest{
		Mode:    "input_suggest",
		Prompt:  inputengine.AIPrompt(raw, appName),
		Profile: &profile,
	}, func(delta string) {
		if reply.Len() < 2048 {
			reply.WriteString(delta)
		}
	})
	if err != nil {
		return nil
	}
	return inputengine.ParseAICandidates(reply.String())
}
