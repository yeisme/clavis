package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/yeisme/clavis/pkg/core/toml"
)

// LoadError represents a file read, TOML parse, schema decode, or validation error.
type LoadError struct {
	File    string `json:"file"`
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	KeyPath string `json:"keyPath,omitempty"`
}

func (e LoadError) Error() string {
	loc := e.File
	if e.Line > 0 && e.Column > 0 {
		loc += fmt.Sprintf(" %d:%d", e.Line, e.Column)
	}
	if e.KeyPath != "" {
		loc += " " + e.KeyPath
	}
	return fmt.Sprintf("[%s] %s", loc, e.Message)
}

// ConfigStore manages thread-safe loading, hot-reloading, and atomic rollback
// to the last-known-good ClavisConfig.
type ConfigStore struct {
	mu           sync.RWMutex
	dir          string
	current      ClavisConfig
	lastErrors   []LoadError
	lastWarnings []ValidationIssue
}

// NewConfigStore creates a ConfigStore rooted at dir with DefaultConfig().
func NewConfigStore(dir string) *ConfigStore {
	return &ConfigStore{
		dir:     dir,
		current: DefaultConfig(),
	}
}

// Directory returns the configuration directory path.
func (s *ConfigStore) Directory() string {
	return s.dir
}

// Current returns the active last-known-good ClavisConfig.
func (s *ConfigStore) Current() ClavisConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// LastErrors returns errors from the most recent failed Reload (empty if last Reload succeeded).
func (s *ConfigStore) LastErrors() []LoadError {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]LoadError, len(s.lastErrors))
	copy(out, s.lastErrors)
	return out
}

// LastWarnings returns validation warnings from the most recent successful Reload.
func (s *ConfigStore) LastWarnings() []ValidationIssue {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ValidationIssue, len(s.lastWarnings))
	copy(out, s.lastWarnings)
	return out
}

// LoadOrInitialize creates default config files if missing and loads them.
func (s *ConfigStore) LoadOrInitialize() (ClavisConfig, error) {
	if _, err := GenerateIfMissing(s.dir); err != nil {
		return ClavisConfig{}, err
	}
	s.Reload()
	return s.Current(), nil
}

// Reload parses and validates all 5 TOML config files from disk.
// On any parse, decode, or validation error, it preserves the previous Current() config
// and records errors in LastErrors(). Returns true if reload succeeded.
func (s *ConfigStore) Reload() bool {
	var errs []LoadError

	mainCfg, ok := loadConfigFile(s.dir, FileMain, DecodeMainConfig, &errs)
	leaderCfg, ok2 := loadConfigFile(s.dir, FileLeader, DecodeLeaderConfig, &errs)
	snippetsCfg, ok3 := loadConfigFile(s.dir, FileSnippets, DecodeSnippetsConfig, &errs)
	glossaryCfg, ok4 := loadConfigFile(s.dir, FileGlossary, DecodeGlossaryConfig, &errs)
	imeRulesCfg, ok5 := loadConfigFile(s.dir, FileIMERules, DecodeIMERulesConfig, &errs)

	if len(errs) > 0 || !ok || !ok2 || !ok3 || !ok4 || !ok5 {
		s.mu.Lock()
		s.lastErrors = errs
		s.mu.Unlock()
		return false
	}

	candidate := ClavisConfig{
		Main:     mainCfg,
		Leader:   leaderCfg,
		Snippets: snippetsCfg,
		Glossary: glossaryCfg,
		IMERules: imeRulesCfg,
	}

	issues := Validate(candidate)
	var errorIssues []ValidationIssue
	var warningIssues []ValidationIssue
	for _, issue := range issues {
		if issue.Severity == SeverityError {
			errorIssues = append(errorIssues, issue)
		} else {
			warningIssues = append(warningIssues, issue)
		}
	}

	if len(errorIssues) > 0 {
		loadErrs := make([]LoadError, len(errorIssues))
		for i, issue := range errorIssues {
			loadErrs[i] = LoadError{
				File:    issue.File,
				Message: issue.Message,
				KeyPath: issue.KeyPath,
			}
		}
		s.mu.Lock()
		s.lastErrors = loadErrs
		s.mu.Unlock()
		return false
	}

	s.mu.Lock()
	s.current = candidate
	s.lastErrors = nil
	s.lastWarnings = warningIssues
	s.mu.Unlock()
	return true
}

func loadConfigFile[T any](dir string, kind ConfigFileKind, decode func(string) (T, error), errs *[]LoadError) (T, bool) {
	var zero T
	filePath := filepath.Join(dir, string(kind))
	data, err := os.ReadFile(filePath)
	if err != nil {
		*errs = append(*errs, LoadError{
			File:    string(kind),
			Message: fmt.Sprintf("Failed to read file: %v", err),
		})
		return zero, false
	}

	cfg, err := decode(string(data))
	if err != nil {
		var pe *toml.ParseError
		var de *DecodingError
		switch {
		case errors.As(err, &pe):
			*errs = append(*errs, LoadError{
				File:    string(kind),
				Message: pe.Message,
				Line:    pe.Line,
				Column:  pe.Column,
			})
		case errors.As(err, &de):
			*errs = append(*errs, LoadError{
				File:    string(kind),
				Message: de.Message,
				KeyPath: de.KeyPath,
			})
		default:
			*errs = append(*errs, LoadError{
				File:    string(kind),
				Message: err.Error(),
			})
		}
		return zero, false
	}
	return cfg, true
}
