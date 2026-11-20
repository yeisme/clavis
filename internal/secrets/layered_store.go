package secrets

import (
	"errors"
	"os"
	"strings"
)

// getenv is a seam for tests; production always reads the real environment.
var getenv = os.Getenv

// LayeredStore reads keys from a 0600 file first and only then from the OS keychain.
// Writes always go to the file.
//
// The point is to stop the macOS authorisation dialog: the keychain ties an item to the code
// signature that stored it, and a rebuilt locally signed app no longer matches, so every read
// prompts SecurityAgent. Reads therefore hit the file, and the keychain is consulted only as a
// migration path for keys stored by an older build — and only until MigrateFromKeychain has
// moved them across.
type LayeredStore struct {
	File     *FileStore
	Keychain APIKeyStore
	// Migrate enables the one-time keychain fallback on read. With it off, a missing file
	// entry means "not configured" and the keychain is never touched (so never prompts).
	Migrate bool
}

var _ APIKeyStore = (*LayeredStore)(nil)

func (s *LayeredStore) file() *FileStore {
	if s.File != nil {
		return s.File
	}
	return &FileStore{}
}

func (s *LayeredStore) ReadKey(account string) (string, error) {
	if s.File != nil {
		v, err := s.File.ReadKey(account)
		if err == nil && v != "" {
			return v, nil
		}
		if err != nil && !errors.Is(err, ErrSecretNotFound) {
			return "", err // a broken or over-permissive file is reported, not papered over
		}
	}
	if s.Migrate && s.Keychain != nil && strings.TrimSpace(account) != "" {
		return s.Keychain.ReadKey(account)
	}
	return "", ErrSecretNotFound
}

// SaveKey always writes the file. It never writes the keychain, so a rebuild can never break
// a key it just stored.
func (s *LayeredStore) SaveKey(account string, apiKey string) error {
	f := s.file()
	if s.File == nil {
		s.File = f
	}
	return f.SaveKey(account, apiKey)
}

func (s *LayeredStore) DeleteKey(account string) error {
	f := s.file()
	if s.File == nil {
		s.File = f
	}
	if err := f.DeleteKey(account); err != nil && !errors.Is(err, ErrSecretNotFound) {
		return err
	}
	// Best effort: drop a legacy keychain copy too, so it cannot resurface via Migrate.
	if s.Keychain != nil && strings.TrimSpace(account) != "" && s.Migrate {
		_ = s.Keychain.DeleteKey(account)
	}
	return nil
}

func (s *LayeredStore) ResolveAPIKey(account string) (string, error) {
	return s.ResolveAPIKeyWithEnv(account, DefaultEnvVar)
}

func (s *LayeredStore) ResolveAPIKeyWithEnv(account string, envVar string) (string, error) {
	if v, err := s.ReadKey(account); err == nil && v != "" {
		return v, nil
	} else if err != nil && !errors.Is(err, ErrSecretNotFound) {
		return "", err
	}
	if envVar = strings.TrimSpace(envVar); envVar != "" {
		if v := strings.TrimSpace(getenv(envVar)); v != "" {
			return v, nil
		}
	}
	if s.Keychain != nil && s.Migrate {
		return s.Keychain.ResolveAPIKeyWithEnv(account, envVar)
	}
	return "", ErrSecretNotFound
}

func (s *LayeredStore) ResolveAPIKeySource(account string) (APIKeySource, error) {
	return s.ResolveAPIKeySourceWithEnv(account, DefaultEnvVar)
}

func (s *LayeredStore) ResolveAPIKeySourceWithEnv(account string, envVar string) (APIKeySource, error) {
	if v, err := s.ReadKey(account); err == nil && v != "" {
		if s.File != nil {
			if fv, ferr := s.File.ReadKey(account); ferr == nil && fv == v {
				return KeySourceFile, nil
			}
		}
		return KeySourceKeychain, nil
	} else if err != nil && !errors.Is(err, ErrSecretNotFound) {
		return KeySourceNone, err
	}
	if envVar = strings.TrimSpace(envVar); envVar != "" {
		if strings.TrimSpace(getenv(envVar)) != "" {
			return KeySourceEnv, nil
		}
	}
	return KeySourceNone, nil
}

// MigrateFromKeychain copies a keychain-stored key into the file and reports whether it moved.
// Callers run it once for each configured profile account; afterwards Migrate can be turned off
// and the keychain is never read again.
func (s *LayeredStore) MigrateFromKeychain(account string) (bool, error) {
	acct := strings.TrimSpace(account)
	if acct == "" || s.Keychain == nil || s.File == nil {
		return false, nil
	}
	if v, err := s.File.ReadKey(acct); err == nil && v != "" {
		return false, nil // already in the file
	}
	v, err := s.Keychain.ReadKey(acct)
	if err != nil {
		if errors.Is(err, ErrSecretNotFound) {
			return false, nil
		}
		return false, err
	}
	if v == "" {
		return false, nil
	}
	if err := s.File.SaveKey(acct, v); err != nil {
		return false, err
	}
	return true, nil
}
