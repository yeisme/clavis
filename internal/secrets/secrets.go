package secrets

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/yeisme/clavis/pkg/core/config"
)

// DefaultEnvVar is the default environment variable name for the primary AI provider.
const DefaultEnvVar = "DEEPSEEK_API_KEY"

// FallbackEnvVar is the generic Clavis API key environment variable fallback.
const FallbackEnvVar = "CLAVIS_API_KEY"

// LegacyOpenAIEnvVar is only used as a last-resort fallback or when explicitly requested.
const LegacyOpenAIEnvVar = "OPENAI_API_KEY"

// ErrSecretNotFound is returned when no API key is found in the credential store or fallback env var.
var ErrSecretNotFound = mygo.ErrSecretNotFound

// APIKeySource describes where a resolved API key came from.
type APIKeySource string

const (
	KeySourceNone     APIKeySource = "none"
	KeySourceKeychain APIKeySource = "keychain"
	KeySourceEnv      APIKeySource = "env"
	// KeySourceFile means the key came from the 0600 keys file rather than the OS keychain.
	// Preferred on macOS, where keychain ACLs are bound to a code signature and a rebuilt
	// locally signed app re-prompts for authorisation on every read.
	KeySourceFile APIKeySource = "file"
)

// DefaultEnvVarForProvider returns the canonical API key environment variable for a provider.
func DefaultEnvVarForProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "anthropic", "claude":
		return "ANTHROPIC_API_KEY"
	case "gemini", "google":
		return "GEMINI_API_KEY"
	case "ollama":
		return ""
	case "openai":
		return LegacyOpenAIEnvVar
	default:
		return FallbackEnvVar
	}
}

// APIKeyStore abstracts credential storage for AI provider API keys.
// Implementations must never log secret values or write them into TOML configuration files.
type APIKeyStore interface {
	ReadKey(account string) (string, error)
	SaveKey(account string, apiKey string) error
	DeleteKey(account string) error
	ResolveAPIKey(account string) (string, error)
	ResolveAPIKeySource(account string) (APIKeySource, error)
	ResolveAPIKeyWithEnv(account string, envVar string) (string, error)
	ResolveAPIKeySourceWithEnv(account string, envVar string) (APIKeySource, error)
}

// MyGoSecretsStore wraps mygo.Secrets (OS Keychain / Credential Manager / Secret Service)
// with an environment-variable fallback.
type MyGoSecretsStore struct {
	Service   string
	EnvVar    string
	EnvLookup func(string) string
}

var _ APIKeyStore = (*MyGoSecretsStore)(nil)

// NewMyGoSecretsStore constructs a MyGoSecretsStore.
// If service is empty, mygo.Secrets uses the application's identifier.
func NewMyGoSecretsStore(service string) *MyGoSecretsStore {
	return &MyGoSecretsStore{
		Service: service,
		EnvVar:  DefaultEnvVar,
	}
}

func (s *MyGoSecretsStore) module() mygo.SecretsModule {
	if s.Service != "" {
		return mygo.Secrets.Service(s.Service)
	}
	return mygo.Secrets
}

func (s *MyGoSecretsStore) lookupEnv(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	lookup := s.EnvLookup
	if lookup == nil {
		lookup = os.Getenv
	}
	return strings.TrimSpace(lookup(trimmed))
}

func (s *MyGoSecretsStore) defaultEnvKey() string {
	name := strings.TrimSpace(s.EnvVar)
	if name == "" {
		name = DefaultEnvVar
	}
	return s.lookupEnv(name)
}

func validateAccount(account string) (string, error) {
	trimmed := strings.TrimSpace(account)
	if trimmed == "" {
		return "", errors.New("secrets: account cannot be empty")
	}
	return trimmed, nil
}

func (s *MyGoSecretsStore) ReadKey(account string) (string, error) {
	acct, err := validateAccount(account)
	if err != nil {
		return "", err
	}
	raw, err := s.module().Get(acct)
	if err != nil {
		return "", err
	}
	val := strings.TrimSpace(string(raw))
	if val == "" {
		return "", ErrSecretNotFound
	}
	return val, nil
}

func (s *MyGoSecretsStore) SaveKey(account string, apiKey string) error {
	acct, err := validateAccount(account)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return errors.New("secrets: API key cannot be empty")
	}
	if err := s.module().Set(acct, []byte(trimmed)); err != nil {
		return fmt.Errorf("secrets: save key: %w", err)
	}
	return nil
}

func (s *MyGoSecretsStore) DeleteKey(account string) error {
	acct, err := validateAccount(account)
	if err != nil {
		return err
	}
	if err := s.module().Delete(acct); err != nil {
		return fmt.Errorf("secrets: delete key: %w", err)
	}
	return nil
}

func (s *MyGoSecretsStore) ResolveAPIKey(account string) (string, error) {
	val, err := s.ReadKey(account)
	if err == nil && val != "" {
		return val, nil
	}
	if err != nil && !errors.Is(err, ErrSecretNotFound) && !errors.Is(err, errors.ErrUnsupported) {
		if envVal := s.defaultEnvKey(); envVal != "" {
			return envVal, nil
		}
		return "", err
	}
	if envVal := s.defaultEnvKey(); envVal != "" {
		return envVal, nil
	}
	return "", ErrSecretNotFound
}

func (s *MyGoSecretsStore) ResolveAPIKeySource(account string) (APIKeySource, error) {
	val, err := s.ReadKey(account)
	if err == nil && val != "" {
		return KeySourceKeychain, nil
	}
	if envVal := s.defaultEnvKey(); envVal != "" {
		return KeySourceEnv, nil
	}
	if err != nil && !errors.Is(err, ErrSecretNotFound) && !errors.Is(err, errors.ErrUnsupported) {
		return KeySourceNone, err
	}
	return KeySourceNone, nil
}

func (s *MyGoSecretsStore) ResolveAPIKeyWithEnv(account string, envVar string) (string, error) {
	var readErr error
	if strings.TrimSpace(account) != "" {
		val, err := s.ReadKey(account)
		if err == nil && val != "" {
			return val, nil
		}
		readErr = err
	}
	if envVal := s.lookupEnv(envVar); envVal != "" {
		return envVal, nil
	}
	if readErr != nil && !errors.Is(readErr, ErrSecretNotFound) && !errors.Is(readErr, errors.ErrUnsupported) {
		return "", readErr
	}
	return "", ErrSecretNotFound
}

func (s *MyGoSecretsStore) ResolveAPIKeySourceWithEnv(account string, envVar string) (APIKeySource, error) {
	var readErr error
	if strings.TrimSpace(account) != "" {
		val, err := s.ReadKey(account)
		if err == nil && val != "" {
			return KeySourceKeychain, nil
		}
		readErr = err
	}
	if envVal := s.lookupEnv(envVar); envVal != "" {
		return KeySourceEnv, nil
	}
	if readErr != nil && !errors.Is(readErr, ErrSecretNotFound) && !errors.Is(readErr, errors.ErrUnsupported) {
		return KeySourceNone, readErr
	}
	return KeySourceNone, nil
}

// MemoryAPIKeyStore is a thread-safe in-memory APIKeyStore for unit tests.
type MemoryAPIKeyStore struct {
	mu        sync.RWMutex
	keys      map[string]string
	EnvVar    string
	EnvLookup func(string) string
}

var _ APIKeyStore = (*MemoryAPIKeyStore)(nil)

// NewMemoryAPIKeyStore constructs an empty MemoryAPIKeyStore.
func NewMemoryAPIKeyStore() *MemoryAPIKeyStore {
	return &MemoryAPIKeyStore{
		keys:   make(map[string]string),
		EnvVar: DefaultEnvVar,
	}
}

func (m *MemoryAPIKeyStore) lookupEnv(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	lookup := m.EnvLookup
	if lookup == nil {
		lookup = os.Getenv
	}
	return strings.TrimSpace(lookup(trimmed))
}

func (m *MemoryAPIKeyStore) defaultEnvKey() string {
	name := strings.TrimSpace(m.EnvVar)
	if name == "" {
		name = DefaultEnvVar
	}
	return m.lookupEnv(name)
}

func (m *MemoryAPIKeyStore) ReadKey(account string) (string, error) {
	acct, err := validateAccount(account)
	if err != nil {
		return "", err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.keys[acct]
	if !ok || strings.TrimSpace(v) == "" {
		return "", ErrSecretNotFound
	}
	return v, nil
}

func (m *MemoryAPIKeyStore) SaveKey(account string, apiKey string) error {
	acct, err := validateAccount(account)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return errors.New("secrets: API key cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[acct] = trimmed
	return nil
}

func (m *MemoryAPIKeyStore) DeleteKey(account string) error {
	acct, err := validateAccount(account)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, acct)
	return nil
}

func (m *MemoryAPIKeyStore) ResolveAPIKey(account string) (string, error) {
	if v, err := m.ReadKey(account); err == nil && v != "" {
		return v, nil
	} else if err != nil && !errors.Is(err, ErrSecretNotFound) {
		return "", err
	}
	if envVal := m.defaultEnvKey(); envVal != "" {
		return envVal, nil
	}
	return "", ErrSecretNotFound
}

func (m *MemoryAPIKeyStore) ResolveAPIKeySource(account string) (APIKeySource, error) {
	if v, err := m.ReadKey(account); err == nil && v != "" {
		return KeySourceKeychain, nil
	} else if err != nil && !errors.Is(err, ErrSecretNotFound) {
		return KeySourceNone, err
	}
	if envVal := m.defaultEnvKey(); envVal != "" {
		return KeySourceEnv, nil
	}
	return KeySourceNone, nil
}

func (m *MemoryAPIKeyStore) ResolveAPIKeyWithEnv(account string, envVar string) (string, error) {
	var readErr error
	if strings.TrimSpace(account) != "" {
		if v, err := m.ReadKey(account); err == nil && v != "" {
			return v, nil
		} else {
			readErr = err
		}
	}
	if envVal := m.lookupEnv(envVar); envVal != "" {
		return envVal, nil
	}
	if readErr != nil && !errors.Is(readErr, ErrSecretNotFound) {
		return "", readErr
	}
	return "", ErrSecretNotFound
}

func (m *MemoryAPIKeyStore) ResolveAPIKeySourceWithEnv(account string, envVar string) (APIKeySource, error) {
	var readErr error
	if strings.TrimSpace(account) != "" {
		if v, err := m.ReadKey(account); err == nil && v != "" {
			return KeySourceKeychain, nil
		} else {
			readErr = err
		}
	}
	if envVal := m.lookupEnv(envVar); envVal != "" {
		return KeySourceEnv, nil
	}
	if readErr != nil && !errors.Is(readErr, ErrSecretNotFound) {
		return KeySourceNone, readErr
	}
	return KeySourceNone, nil
}

func isKeylessProfile(profile config.AIProfile) bool {
	if strings.EqualFold(strings.TrimSpace(profile.Provider), "ollama") {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(profile.BaseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func profileEffectiveEnvVar(profile config.AIProfile) string {
	envVar := strings.TrimSpace(profile.APIKeyEnvVar)
	if envVar != "" {
		return envVar
	}
	return DefaultEnvVarForProvider(profile.Provider)
}

// ResolveProfileAPIKey resolves the API key for a specific AIProfile:
// 1. Checks store.ReadKey(profile.KeychainAccount) first;
// 2. Falls back to profile.APIKeyEnvVar (or DefaultEnvVarForProvider(profile.Provider));
// 3. Returns ("", nil) when keyless operation is allowed (e.g. local Ollama / localhost / 127.0.0.1 / ::1).
func ResolveProfileAPIKey(store APIKeyStore, profile config.AIProfile) (string, error) {
	acct := strings.TrimSpace(profile.KeychainAccount)
	envVar := profileEffectiveEnvVar(profile)

	if store != nil {
		if acct != "" {
			if key, err := store.ReadKey(acct); err == nil && key != "" {
				return key, nil
			}
		}
		if key, err := store.ResolveAPIKeyWithEnv(acct, envVar); err == nil && key != "" {
			return key, nil
		} else if err != nil && !errors.Is(err, ErrSecretNotFound) && !errors.Is(err, errors.ErrUnsupported) {
			if isKeylessProfile(profile) {
				return "", nil
			}
			return "", err
		}
	} else if envVar != "" {
		if val := strings.TrimSpace(os.Getenv(envVar)); val != "" {
			return val, nil
		}
	}

	if isKeylessProfile(profile) {
		return "", nil
	}
	return "", ErrSecretNotFound
}

// ResolveProfileAPIKeySource resolves the APIKeySource for a specific AIProfile.
func ResolveProfileAPIKeySource(store APIKeyStore, profile config.AIProfile) (APIKeySource, error) {
	acct := strings.TrimSpace(profile.KeychainAccount)
	envVar := profileEffectiveEnvVar(profile)

	if store != nil {
		if acct != "" {
			if key, err := store.ReadKey(acct); err == nil && key != "" {
				return KeySourceKeychain, nil
			}
		}
		src, err := store.ResolveAPIKeySourceWithEnv(acct, envVar)
		if err == nil && src != KeySourceNone {
			return src, nil
		}
		if err != nil && !errors.Is(err, ErrSecretNotFound) && !errors.Is(err, errors.ErrUnsupported) {
			if isKeylessProfile(profile) {
				return KeySourceNone, nil
			}
			return KeySourceNone, err
		}
		return KeySourceNone, nil
	}

	if envVar != "" && strings.TrimSpace(os.Getenv(envVar)) != "" {
		return KeySourceEnv, nil
	}
	return KeySourceNone, nil
}
