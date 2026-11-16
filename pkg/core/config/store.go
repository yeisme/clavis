package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yeisme/clavis/pkg/core/i18n"
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
	savedDigests map[string][32]byte
}

// NewConfigStore creates a ConfigStore rooted at dir with DefaultConfig().
func NewConfigStore(dir string) *ConfigStore {
	return &ConfigStore{
		dir:          dir,
		current:      DefaultConfig(),
		savedDigests: make(map[string][32]byte),
	}
}

// SavedDigests returns a copy of the SHA-256 content digests of the last successfully
// loaded or atomically saved TOML files in Directory().
func (s *ConfigStore) SavedDigests() map[string][32]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.savedDigests) == 0 {
		return nil
	}
	out := make(map[string][32]byte, len(s.savedDigests))
	maps.Copy(out, s.savedDigests)
	return out
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

func firstErrorIssue(issues []ValidationIssue) error {
	for _, issue := range issues {
		if issue.Severity == SeverityError {
			return LoadError{
				File:    issue.File,
				KeyPath: issue.KeyPath,
				Message: issue.Message,
			}
		}
	}
	return nil
}

func collectWarningIssues(issues []ValidationIssue) []ValidationIssue {
	var warnings []ValidationIssue
	for _, issue := range issues {
		if issue.Severity != SeverityError {
			warnings = append(warnings, issue)
		}
	}
	return warnings
}

func (s *ConfigStore) ensureSavedDigestsLocked() {
	if s.savedDigests == nil {
		s.savedDigests = make(map[string][32]byte)
	}
	if len(s.savedDigests) == 0 {
		maps.Copy(s.savedDigests, readTOMLFileHashes(s.dir))
	}
}

func readTOMLFileHashes(dir string) map[string][32]byte {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make(map[string][32]byte)
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".toml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		out[entry.Name()] = sha256.Sum256(data)
	}
	return out
}

// SaveMain validates mainCfg, atomically writes config.toml to disk, and updates Current().Main.
func (s *ConfigStore) SaveMain(mainCfg MainConfig) error {
	if err := firstErrorIssue(ValidateMain(mainCfg)); err != nil {
		return err
	}
	encoded := mainCfg.Encode()
	if err := atomicWriteConfigFile(s.dir, FileMain, encoded); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(encoded))
	s.mu.Lock()
	s.ensureSavedDigestsLocked()
	s.current.Main = mainCfg
	s.lastErrors = nil
	s.lastWarnings = collectWarningIssues(Validate(s.current))
	s.savedDigests[string(FileMain)] = sum
	s.mu.Unlock()
	return nil
}

// SaveLeader validates leaderCfg (including cross-file snippet references), atomically writes leader.toml, and updates Current().Leader.
func (s *ConfigStore) SaveLeader(leaderCfg LeaderConfig) error {
	cur := s.Current()
	knownSnippetIDs := make(map[string]struct{}, len(cur.Snippets.Items))
	for _, item := range cur.Snippets.Items {
		knownSnippetIDs[item.ID] = struct{}{}
	}
	if err := firstErrorIssue(ValidateLeader(leaderCfg, knownSnippetIDs)); err != nil {
		return err
	}
	encoded := leaderCfg.Encode()
	if err := atomicWriteConfigFile(s.dir, FileLeader, encoded); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(encoded))
	s.mu.Lock()
	s.ensureSavedDigestsLocked()
	s.current.Leader = leaderCfg
	s.lastErrors = nil
	s.lastWarnings = collectWarningIssues(Validate(s.current))
	s.savedDigests[string(FileLeader)] = sum
	s.mu.Unlock()
	return nil
}

// SaveSnippets validates snippetsCfg, atomically writes snippets.toml, and updates Current().Snippets and cross-file warnings.
func (s *ConfigStore) SaveSnippets(snippetsCfg SnippetsConfig) error {
	if err := firstErrorIssue(ValidateSnippets(snippetsCfg)); err != nil {
		return err
	}
	encoded := snippetsCfg.Encode()
	if err := atomicWriteConfigFile(s.dir, FileSnippets, encoded); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(encoded))
	s.mu.Lock()
	s.ensureSavedDigestsLocked()
	s.current.Snippets = snippetsCfg
	s.lastErrors = nil
	s.lastWarnings = collectWarningIssues(Validate(s.current))
	s.savedDigests[string(FileSnippets)] = sum
	s.mu.Unlock()
	return nil
}

// SaveGlossary validates glossaryCfg, atomically writes glossary.toml, and updates Current().Glossary.
func (s *ConfigStore) SaveGlossary(glossaryCfg GlossaryConfig) error {
	if err := firstErrorIssue(ValidateGlossary(glossaryCfg)); err != nil {
		return err
	}
	encoded := glossaryCfg.Encode()
	if err := atomicWriteConfigFile(s.dir, FileGlossary, encoded); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(encoded))
	s.mu.Lock()
	s.ensureSavedDigestsLocked()
	s.current.Glossary = glossaryCfg
	s.lastErrors = nil
	s.lastWarnings = collectWarningIssues(Validate(s.current))
	s.savedDigests[string(FileGlossary)] = sum
	s.mu.Unlock()
	return nil
}

// SaveIMERules validates imeRulesCfg, atomically writes ime-rules.toml to disk, and updates Current().IMERules.
func (s *ConfigStore) SavePrompts(cfg PromptsConfig) error {
	if err := firstErrorIssue(ValidatePrompts(cfg)); err != nil {
		return err
	}
	if err := atomicWriteConfigFile(s.dir, FilePrompts, cfg.Encode()); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureSavedDigestsLocked()
	s.current.Prompts = cfg
	s.lastErrors = nil
	s.savedDigests[string(FilePrompts)] = sha256.Sum256([]byte(cfg.Encode()))
	s.mu.Unlock()
	return nil
}
func (s *ConfigStore) SavePromptMarkdown(id, content string) error {
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" || trimmedID != filepath.Base(trimmedID) || strings.Contains(trimmedID, string(filepath.Separator)) {
		return errors.New("prompt id must be a non-empty file name")
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "prompts"), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(s.dir, "prompts", trimmedID+".md"), content)
}
func (s *ConfigStore) SaveComponents(cfg ComponentsConfig) error {
	if err := firstErrorIssue(ValidateComponents(cfg)); err != nil {
		return err
	}
	if err := atomicWriteConfigFile(s.dir, FileComponents, cfg.Encode()); err != nil {
		return err
	}
	s.mu.Lock()
	s.ensureSavedDigestsLocked()
	s.current.Components = cfg
	s.lastErrors = nil
	s.savedDigests[string(FileComponents)] = sha256.Sum256([]byte(cfg.Encode()))
	s.mu.Unlock()
	return nil
}

func (s *ConfigStore) SaveIMERules(imeRulesCfg IMERulesConfig) error {
	if err := firstErrorIssue(ValidateIMERules(imeRulesCfg)); err != nil {
		return err
	}
	encoded := imeRulesCfg.Encode()
	if err := atomicWriteConfigFile(s.dir, FileIMERules, encoded); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(encoded))
	s.mu.Lock()
	s.ensureSavedDigestsLocked()
	s.current.IMERules = imeRulesCfg
	s.lastErrors = nil
	s.lastWarnings = collectWarningIssues(Validate(s.current))
	s.savedDigests[string(FileIMERules)] = sum
	s.mu.Unlock()
	return nil
}

// SaveAll validates the full ClavisConfig across all 5 files before atomically persisting every TOML file.
func (s *ConfigStore) SaveAll(cfg ClavisConfig) error {
	issues := Validate(cfg)
	if err := firstErrorIssue(issues); err != nil {
		return err
	}
	files := []struct {
		kind    ConfigFileKind
		content string
	}{
		{FileMain, cfg.Main.Encode()},
		{FileLeader, cfg.Leader.Encode()},
		{FileSnippets, cfg.Snippets.Encode()},
		{FileGlossary, cfg.Glossary.Encode()},
		{FileIMERules, cfg.IMERules.Encode()},
		{FilePrompts, cfg.Prompts.Encode()},
		{FileComponents, cfg.Components.Encode()},
	}
	sums := make(map[string][32]byte, len(files))
	for _, f := range files {
		if err := atomicWriteConfigFile(s.dir, f.kind, f.content); err != nil {
			return err
		}
		sums[string(f.kind)] = sha256.Sum256([]byte(f.content))
	}
	warnings := collectWarningIssues(issues)
	s.mu.Lock()
	s.ensureSavedDigestsLocked()
	s.current = cfg
	s.lastErrors = nil
	s.lastWarnings = warnings
	maps.Copy(s.savedDigests, sums)
	s.mu.Unlock()
	return nil
}

func atomicWriteFile(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".clavis-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func atomicWriteConfigFile(dir string, kind ConfigFileKind, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".clavis-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return err
	}
	targetPath := filepath.Join(dir, string(kind))
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return err
	}
	cleanup = false
	return nil
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
	promptsCfg, ok6 := loadConfigFile(s.dir, FilePrompts, DecodePromptsConfig, &errs)
	componentsCfg, ok7 := loadConfigFile(s.dir, FileComponents, DecodeComponentsConfig, &errs)
	if ok6 {
		var mergeErr error
		promptsCfg, mergeErr = mergePromptMarkdown(promptsCfg, s.dir)
		if mergeErr != nil {
			errs = append(errs, LoadError{File: "prompts/*.md", Message: mergeErr.Error()})
		}
	}
	if ok7 {
		var mergeErr error
		componentsCfg, mergeErr = mergeComponentScripts(componentsCfg, s.dir)
		if mergeErr != nil {
			errs = append(errs, LoadError{File: "components/*.toml", Message: mergeErr.Error()})
		}
	}

	if len(errs) > 0 || !ok || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !ok7 {
		s.mu.Lock()
		s.lastErrors = errs
		s.mu.Unlock()
		return false
	}

	candidate := ClavisConfig{
		Main:       mainCfg,
		Leader:     leaderCfg,
		Snippets:   snippetsCfg,
		Glossary:   glossaryCfg,
		IMERules:   imeRulesCfg,
		Prompts:    promptsCfg,
		Components: componentsCfg,
	}

	// Speak the language of the config being validated so its diagnostics
	// localize; the previous language stays until a config decodes cleanly.
	i18n.SyncConfigLanguage(candidate.Main.Appearance.Language)

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

	diskHashes := readTOMLFileHashes(s.dir)
	// The validator above saw the raw stage_intensity so it could warn about an
	// out-of-range value; the running config gets the clamped one, so no view
	// has to re-check the range.
	candidate.Main.Appearance.StageIntensity = NormalizeStageIntensity(candidate.Main.Appearance.StageIntensity)
	s.mu.Lock()
	s.current = candidate
	s.lastErrors = nil
	s.lastWarnings = warningIssues
	if diskHashes != nil {
		s.savedDigests = diskHashes
	}
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
			Message: i18n.Tf("decode.store.read_failed", err),
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
