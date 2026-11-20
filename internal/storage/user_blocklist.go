package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/egoist/mygo/plugins/sqlite"
)

// UserBlocklistStore persists exact candidate words hidden by the user. The in-memory copy is
// read by the input hook, while writes happen from settings or the UI thread.
type UserBlocklistStore struct {
	db    *sqlite.DB
	mu    sync.RWMutex
	words map[string]struct{}
}

func NewUserBlocklistStore(db *sqlite.DB) (*UserBlocklistStore, error) {
	if db == nil {
		return nil, fmt.Errorf("nil sqlite database")
	}
	s := &UserBlocklistStore{db: db, words: make(map[string]struct{})}
	if _, err := db.Execute(context.Background(), `CREATE TABLE IF NOT EXISTS ime_user_blocklist (word TEXT PRIMARY KEY NOT NULL)`); err != nil {
		return nil, fmt.Errorf("init IME blocklist: %w", err)
	}
	rows, err := db.Query(context.Background(), `SELECT word FROM ime_user_blocklist ORDER BY word`)
	if err != nil {
		return nil, err
	}
	for _, row := range rows.Values {
		if len(row) > 0 {
			if word := strings.TrimSpace(str(row, 0)); word != "" {
				s.words[word] = struct{}{}
			}
		}
	}
	return s, nil
}

func (s *UserBlocklistStore) Words() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	out := make([]string, 0, len(s.words))
	for word := range s.words {
		out = append(out, word)
	}
	s.mu.RUnlock()
	sort.Strings(out)
	return out
}

func (s *UserBlocklistStore) Block(ctx context.Context, word string) error {
	if s == nil || s.db == nil {
		return nil
	}
	word = strings.TrimSpace(word)
	if word == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := s.db.Execute(ctx, `INSERT OR IGNORE INTO ime_user_blocklist(word) VALUES(?)`, word); err != nil {
		return err
	}
	s.mu.Lock()
	s.words[word] = struct{}{}
	s.mu.Unlock()
	return nil
}

func (s *UserBlocklistStore) Unblock(ctx context.Context, word string) error {
	if s == nil || s.db == nil {
		return nil
	}
	word = strings.TrimSpace(word)
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := s.db.Execute(ctx, `DELETE FROM ime_user_blocklist WHERE word=?`, word); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.words, word)
	s.mu.Unlock()
	return nil
}
