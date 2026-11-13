package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yeisme/clavis/pkg/core/ai"
	"github.com/yeisme/clavis/pkg/core/config"
	"github.com/yeisme/clavis/pkg/core/engine"
)

// StreamCallback receives streaming AI completion events.
// All methods use gomobile-compatible primitive types.
type StreamCallback interface {
	OnDelta(chunk string)
	OnComplete()
	OnError(err string)
}

// CancelToken cancels an active streaming AI request.
type CancelToken interface {
	Cancel()
}

type cancelTokenImpl struct {
	cancel context.CancelFunc
}

func (c *cancelTokenImpl) Cancel() {
	if c != nil && c.cancel != nil {
		c.cancel()
	}
}

// Session holds the stateful mobile/embedded Clavis core session.
// All fields are unexported to keep the gomobile binding surface clean.
type Session struct {
	mu       sync.Mutex
	store    *config.ConfigStore
	machine  *engine.StateMachine
	snippets *engine.SnippetEngine
	glossary engine.GlossaryEngine
	httpDoer ai.HTTPDoer
}

// EventResult is serialized to JSON by HandleEventJSON.
type eventResult struct {
	State   engine.State    `json:"state"`
	Effects []engine.Effect `json:"effects"`
}

// NewSession initializes config files in configDir (if missing) and creates a Session.
func NewSession(configDir string) (*Session, error) {
	if strings.TrimSpace(configDir) == "" {
		return nil, errors.New("configDir cannot be empty")
	}
	store := config.NewConfigStore(configDir)
	cfg, err := store.LoadOrInitialize()
	if err != nil {
		return nil, err
	}
	return &Session{
		store:    store,
		machine:  engine.NewStateMachine(cfg),
		snippets: engine.NewSnippetEngineFromConfig(cfg.Snippets),
		glossary: engine.NewGlossaryEngineFromConfig(cfg.Glossary),
	}, nil
}

// ReloadConfig reloads TOML configuration files from disk, updating engines if valid.
func (s *Session) ReloadConfig() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok := s.store.Reload(); !ok {
		errs := s.store.LastErrors()
		if len(errs) > 0 {
			return errors.New(errs[0].Error())
		}
		return errors.New("failed to reload configuration")
	}
	cfg := s.store.Current()
	s.machine.SetConfig(cfg)
	s.snippets.UpdateSnippets(cfg.Snippets.Items)
	s.glossary = engine.NewGlossaryEngineFromConfig(cfg.Glossary)
	return nil
}

// StateJSON returns the current Capsule state encoded as JSON.
func (s *Session) StateJSON() (string, error) {
	s.mu.Lock()
	st := s.machine.State()
	s.mu.Unlock()
	raw, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ConfigJSON returns the active ClavisConfig encoded as JSON.
func (s *Session) ConfigJSON() (string, error) {
	s.mu.Lock()
	cfg := s.store.Current()
	s.mu.Unlock()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// HandleEventJSON parses an engine.Event JSON payload, advances the Capsule state machine,
// and returns JSON containing {"state": ..., "effects": [...]}.
func (s *Session) HandleEventJSON(eventJSON string) (string, error) {
	var ev engine.Event
	if err := json.Unmarshal([]byte(eventJSON), &ev); err != nil {
		return "", err
	}

	s.mu.Lock()
	effects := s.machine.Handle(ev)
	st := s.machine.State()
	s.mu.Unlock()

	if effects == nil {
		effects = []engine.Effect{}
	}
	res := eventResult{
		State:   st,
		Effects: effects,
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// FeedSnippetRune feeds the first rune of char into the SnippetEngine.
// If a snippet matches, it returns the JSON-encoded engine.SnippetMatch; otherwise "".
func (s *Session) FeedSnippetRune(char string, appID string, clipboard string, appName string) (string, error) {
	if char == "" {
		return "", nil
	}
	r, _ := utf8.DecodeRuneInString(char)
	if r == utf8.RuneError && len(char) == 1 {
		return "", errors.New("invalid UTF-8 character")
	}

	s.mu.Lock()
	match, ok := s.snippets.Feed(r, engine.FeedOptions{
		BundleID:      appID,
		ProcessName:   appID,
		Now:           time.Now(),
		ClipboardText: clipboard,
		AppName:       appName,
	})
	s.mu.Unlock()

	if !ok || match == nil {
		return "", nil
	}
	raw, err := json.Marshal(match)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// BackspaceSnippet removes the last typed rune from the SnippetEngine buffer.
func (s *Session) BackspaceSnippet() {
	s.mu.Lock()
	s.snippets.Backspace()
	s.mu.Unlock()
}

// ResetSnippet clears the SnippetEngine buffer.
func (s *Session) ResetSnippet() {
	s.mu.Lock()
	s.snippets.Reset()
	s.mu.Unlock()
}

// NormalizeGlossary normalizes glossary aliases in text to canonical terms.
func (s *Session) NormalizeGlossary(text string) string {
	s.mu.Lock()
	ge := s.glossary
	s.mu.Unlock()
	return ge.NormalizeAliases(text, true, true)
}

// SwitchAIProfile switches the active AI profile by ID, persists it to config.toml,
// and updates the in-memory state machine.
func (s *Session) SwitchAIProfile(profileID string) error {
	trimmed := strings.TrimSpace(profileID)
	if trimmed == "" {
		return errors.New("profileID cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.store.Current()
	profile, ok := cfg.Main.AI.ProfileByID(trimmed)
	if !ok {
		return errors.New("unknown AI profile: " + trimmed)
	}

	mainCfg := cfg.Main
	mainCfg.AI.ActiveProfileID = profile.ID
	mainCfg.AI.Provider = profile.Provider
	mainCfg.AI.Protocol = profile.Protocol
	mainCfg.AI.BaseURL = profile.BaseURL
	mainCfg.AI.Model = profile.Model
	mainCfg.AI.Temperature = profile.Temperature
	mainCfg.AI.TimeoutSeconds = profile.TimeoutSeconds
	mainCfg.AI.SystemPrompt = profile.SystemPrompt
	mainCfg.AI.KeychainAccount = profile.KeychainAccount
	mainCfg.AI.APIKeyEnvVar = profile.APIKeyEnvVar

	if err := s.store.SaveMain(mainCfg); err != nil {
		return err
	}
	updatedCfg := s.store.Current()
	s.machine.SetConfig(updatedCfg)
	_ = s.machine.Handle(engine.Event{
		Type:      engine.EventSelectAIProfile,
		ProfileID: profile.ID,
	})
	return nil
}

// StreamAI starts an asynchronous OpenAI-compatible SSE streaming completion and delivers
// deltas/completion/errors to cb. Returns a CancelToken to abort the request.
func (s *Session) StreamAI(
	mode string,
	prompt string,
	sourceText string,
	contextJSON string,
	apiKey string,
	cb StreamCallback,
) CancelToken {
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	cfg := s.store.Current()
	ge := s.glossary
	doer := s.httpDoer
	s.mu.Unlock()

	var capsuleCtx *engine.CapsuleContext
	if strings.TrimSpace(contextJSON) != "" {
		var parsed engine.CapsuleContext
		if err := json.Unmarshal([]byte(contextJSON), &parsed); err != nil {
			if cb != nil {
				cb.OnError("invalid contextJSON: " + err.Error())
			}
			cancel()
			return &cancelTokenImpl{cancel: cancel}
		}
		capsuleCtx = &parsed
	}

	client := ai.NewOpenAICompatibleClient(cfg.Main.AI, ge, doer, func(context.Context) (string, error) {
		return apiKey, nil
	})

	req := ai.CompletionRequest{
		Mode:       mode,
		Prompt:     prompt,
		SourceText: sourceText,
		Context:    capsuleCtx,
	}

	go func() {
		defer cancel()
		err := client.StreamCompletion(ctx, req, func(delta string) {
			if cb != nil {
				cb.OnDelta(delta)
			}
		})
		if err != nil {
			if cb != nil {
				cb.OnError(err.Error())
			}
			return
		}
		if cb != nil {
			cb.OnComplete()
		}
	}()

	return &cancelTokenImpl{cancel: cancel}
}
