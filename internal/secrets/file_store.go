package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/yeisme/clavis/pkg/core/toml"
)

// FileStore keeps API keys in a 0600 TOML file instead of the OS keychain.
//
// Why this exists: on macOS the keychain records which *code signature* may read an item.
// A locally signed build gets a new cdhash on every rebuild, so the stored ACL no longer
// matches and macOS raises a SecurityAgent authorisation dialog every single time a key is
// read. That dialog appears on the AI path, which runs while the user is typing — it was
// interrupting typing repeatedly. A file guarded by 0600 has no such coupling: rebuilding
// the app cannot revoke it.
//
// It is deliberately a separate file from config.toml: users share config.toml when asking
// for help, and a key must not ride along in that paste.
type FileStore struct {
	Path  string
	mu    sync.Mutex
	cache map[string]string
	// readErr keeps the last load failure so Resolve* can report a bad file rather than
	// silently looking like "no key configured".
	readErr error
}

const keysFileMode = 0o600

var _ APIKeyStore = (*FileStore)(nil)

// NewFileStore returns a store backed by path. It does not touch the disk until used.
func NewFileStore(path string) *FileStore {
	return &FileStore{Path: path}
}

// DefaultKeysPath is <configDir>/ai-keys.toml.
func DefaultKeysPath(configDir string) string {
	if strings.TrimSpace(configDir) == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			configDir = filepath.Join(home, ".config", "clavis")
		} else {
			configDir = ".clavis"
		}
	}
	return filepath.Join(configDir, "ai-keys.toml")
}

// load reads and parses the file once. A missing file is not an error: it means no keys are
// stored yet, which is the normal first-run state.
func (s *FileStore) load() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache != nil || s.readErr != nil {
		return s.cache
	}
	s.cache = map[string]string{}
	if strings.TrimSpace(s.Path) == "" {
		s.readErr = errors.New("secrets: keys file path is empty")
		return s.cache
	}
	info, err := os.Stat(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.cache
		}
		s.readErr = err
		return s.cache
	}
	// Refuse a file other users can read. Failing loudly beats quietly leaking a key to the
	// whole machine.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		s.readErr = fmt.Errorf("secrets: %s is mode %04o, want 0600 (run: chmod 600 %s)",
			s.Path, perm, s.Path)
		return s.cache
	}
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		s.readErr = err
		return s.cache
	}
	tbl, err := toml.Parse(string(raw))
	if err != nil {
		s.readErr = fmt.Errorf("secrets: parse %s: %w", s.Path, err)
		return s.cache
	}
	section, ok := tbl.Get("keys")
	if !ok {
		return s.cache
	}
	kv, ok := section.AsTable()
	if !ok {
		s.readErr = fmt.Errorf("secrets: %s: [keys] must be a table", s.Path)
		return s.cache
	}
	s.collectKeys(kv, "")
	return s.cache
}

// flush writes the cache back atomically with 0600 from the first byte, so the key is never
// briefly world-readable.
func (s *FileStore) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.Path) == "" {
		return errors.New("secrets: keys file path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tbl := toml.NewTable()
	keys := toml.NewTable()
	names := make([]string, 0, len(s.cache))
	for k := range s.cache {
		names = append(names, k)
	}
	sort.Strings(names) // stable on-disk order keeps diffs readable
	for _, k := range names {
		// The encoder quotes keys containing dots on its own, which is required: an unquoted
		// dotted key (clavis.ai.magpie) parses back as nested tables and the account name
		// would silently become just "clavis".
		keys.Set(k, toml.StringVal(s.cache[k]))
	}
	tbl.Set("keys", toml.TableVal(keys))

	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".ai-keys-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(keysFileMode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(toml.Encode(tbl)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.Path)
}

// collectKeys flattens [keys] into account names. A dotted account written without quotes
// arrives as nested tables (clavis.ai.magpie -> clavis -> ai -> magpie), so nesting is joined
// back with dots instead of being read as a different account.
func (s *FileStore) collectKeys(tbl *toml.Table, prefix string) {
	if tbl == nil {
		return
	}
	for _, k := range tbl.SortedKeys() {
		v, ok := tbl.Get(k)
		if !ok {
			continue
		}
		name := k
		if prefix != "" {
			name = prefix + "." + k
		}
		if str, ok := v.AsString(); ok {
			if trimmed := strings.TrimSpace(str); trimmed != "" {
				s.cache[name] = trimmed
			}
			continue
		}
		if sub, ok := v.AsTable(); ok {
			s.collectKeys(sub, name)
		}
	}
}

func (s *FileStore) ReadKey(account string) (string, error) {
	acct, err := validateAccount(account)
	if err != nil {
		return "", err
	}
	keys := s.load()
	if s.readErr != nil {
		return "", s.readErr
	}
	if v, ok := keys[acct]; ok && v != "" {
		return v, nil
	}
	return "", ErrSecretNotFound
}

func (s *FileStore) SaveKey(account string, apiKey string) error {
	acct, err := validateAccount(account)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return errors.New("secrets: API key cannot be empty")
	}
	s.load() // populate the cache before mutating it
	if s.readErr != nil {
		return s.readErr
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]string{}
	}
	s.cache[acct] = trimmed
	s.readErr = nil
	s.mu.Unlock()
	return s.flush()
}

func (s *FileStore) DeleteKey(account string) error {
	acct, err := validateAccount(account)
	if err != nil {
		return err
	}
	s.load()
	if s.readErr != nil {
		return s.readErr
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]string{}
	}
	delete(s.cache, acct)
	s.mu.Unlock()
	return s.flush()
}

func (s *FileStore) ResolveAPIKey(account string) (string, error) {
	return s.ResolveAPIKeyWithEnv(account, DefaultEnvVar)
}

func (s *FileStore) ResolveAPIKeyWithEnv(account string, envVar string) (string, error) {
	if strings.TrimSpace(account) != "" {
		if v, err := s.ReadKey(account); err == nil && v != "" {
			return v, nil
		} else if err != nil && !errors.Is(err, ErrSecretNotFound) {
			// A malformed or over-permissive file must not be masked by an env fallback:
			// silently using a different key is harder to debug than reporting the file.
			return "", err
		}
	}
	if envVar = strings.TrimSpace(envVar); envVar != "" {
		if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
			return v, nil
		}
	}
	return "", ErrSecretNotFound
}

func (s *FileStore) ResolveAPIKeySource(account string) (APIKeySource, error) {
	return s.ResolveAPIKeySourceWithEnv(account, DefaultEnvVar)
}

func (s *FileStore) ResolveAPIKeySourceWithEnv(account string, envVar string) (APIKeySource, error) {
	if strings.TrimSpace(account) != "" {
		if v, err := s.ReadKey(account); err == nil && v != "" {
			return KeySourceFile, nil
		} else if err != nil && !errors.Is(err, ErrSecretNotFound) {
			return KeySourceNone, err
		}
	}
	if envVar = strings.TrimSpace(envVar); envVar != "" {
		if strings.TrimSpace(os.Getenv(envVar)) != "" {
			return KeySourceEnv, nil
		}
	}
	return KeySourceNone, nil
}

// Err returns the load error, if the keys file exists but cannot be used.
func (s *FileStore) Err() error {
	s.load()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readErr
}
