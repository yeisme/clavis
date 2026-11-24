package storage

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/plugins/sqlite"
	"github.com/yeisme/clavis/pkg/core/inputmigration"
)

const (
	// DefaultLearningSnapshotDevices is used when the caller has not configured
	// an explicit installation allow-list.
	DefaultLearningSnapshotDevices = "mini,windows"
	learningSnapshotSuffix         = ".userdb.txt"

	maxPortableEntries   = 4096
	maxPortableFrequency = uint64(4096)
)

// LearningSnapshotDiagnostic is emitted for a snapshot that needs operator
// attention. Unknown devices are deliberately diagnostics, not silent skips.
type LearningSnapshotDiagnostic struct {
	Device  string
	Path    string
	Kind    string
	Message string
}

// PortableWordEntry is a non-hashed learned word record extracted from
// per-device userdb snapshots for hydration into a local LearningStore.
type PortableWordEntry struct {
	PinyinKey  string
	Word       string
	Frequency  uint64
	Pinned     bool
	LastUsedNS int64
}

// PortableBigramEntry is a non-hashed learned bigram record extracted from
// per-device userdb snapshots for hydration into a local LearningStore.
type PortableBigramEntry struct {
	PrevWord   string
	Word       string
	Frequency  uint64
	LastUsedNS int64
}

// PortableLearningSnapshot is the non-hashed, deterministically merged view of
// approved per-device userdb snapshots that can be hydrated back into a
// LearningStore without losing plaintext words, readings, or pinned state.
type PortableLearningSnapshot struct {
	Devices []string
	Words   []PortableWordEntry
	Bigrams []PortableBigramEntry
}

// LearningSnapshotRead is the read-time result of merging the approved device
// files. Learning holds the privacy-preserving SummaryOnly digest summary;
// Portable holds the non-hashed merged records for local LearningStore hydration.
// PendingDevices contains files that were found but are not in the approved list.
type LearningSnapshotRead struct {
	Learning       inputmigration.LearningData
	Portable       PortableLearningSnapshot
	PendingDevices []string
	Diagnostics    []LearningSnapshotDiagnostic
}

// WriteDeviceLearningSnapshot atomically writes only device's own snapshot.
// The wire records follow Rime's word<TAB>pinyin<TAB>weight format. Clavis
// metadata is kept in comments so ordinary Rime readers can still consume the
// word records while pinned/bigram data remains round-trippable for Clavis.
func (s *LearningStore) WriteDeviceLearningSnapshot(dir, device string) error {
	if s == nil {
		return errors.New("nil learning store")
	}
	if err := validateSnapshotDevice(device); err != nil {
		return err
	}
	if strings.TrimSpace(dir) == "" {
		return errors.New("empty learning snapshot directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create learning snapshot directory: %w", err)
	}

	lines := s.snapshotLines()
	target := filepath.Join(dir, device+learningSnapshotSuffix)
	tmp, err := os.CreateTemp(dir, "."+device+learningSnapshotSuffix+".*")
	if err != nil {
		return fmt.Errorf("create learning snapshot temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("protect learning snapshot: %w", err)
	}
	w := bufio.NewWriter(tmp)
	for _, line := range lines {
		if _, err := w.WriteString(line + "\n"); err != nil {
			cleanup()
			return fmt.Errorf("write learning snapshot: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		cleanup()
		return fmt.Errorf("flush learning snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync learning snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close learning snapshot: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace learning snapshot: %w", err)
	}
	return nil
}

// WriteLearningSnapshot is the package-level spelling for callers that do not
// need a method value.
func WriteLearningSnapshot(store *LearningStore, dir, device string) error {
	return store.WriteDeviceLearningSnapshot(dir, device)
}

// ReadMergedLearningSnapshots scans approved per-device files and merges both
// their SummaryOnly digest summaries (via inputmigration.MergeLearning) and
// their non-hashed PortableLearningSnapshot records. Unknown files are returned
// in PendingDevices and reported through report; they are never silently
// incorporated or discarded.
func ReadMergedLearningSnapshots(dir string, knownDevices []string, report func(LearningSnapshotDiagnostic)) (LearningSnapshotRead, error) {
	known := make(map[string]struct{})
	if len(knownDevices) == 0 {
		knownDevices = strings.Split(DefaultLearningSnapshotDevices, ",")
	}
	for _, device := range knownDevices {
		device = strings.TrimSpace(device)
		if device != "" {
			known[device] = struct{}{}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return LearningSnapshotRead{Learning: inputmigration.LearningData{SummaryOnly: true}}, nil
		}
		return LearningSnapshotRead{}, fmt.Errorf("scan learning snapshots: %w", err)
	}

	var devices []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), learningSnapshotSuffix) {
			continue
		}
		device := strings.TrimSuffix(entry.Name(), learningSnapshotSuffix)
		if err := validateSnapshotDevice(device); err != nil {
			continue
		}
		devices = append(devices, device)
	}
	sort.Strings(devices)
	result := LearningSnapshotRead{Learning: inputmigration.LearningData{SummaryOnly: true}}
	for _, device := range devices {
		path := filepath.Join(dir, device+learningSnapshotSuffix)
		if _, ok := known[device]; !ok {
			result.PendingDevices = append(result.PendingDevices, device)
			result.Diagnostics = append(result.Diagnostics, LearningSnapshotDiagnostic{
				Device: device, Path: path, Kind: "unknown_device",
				Message: fmt.Sprintf("snapshot %q is awaiting device approval", device),
			})
			if report != nil {
				report(result.Diagnostics[len(result.Diagnostics)-1])
			}
			continue
		}
		learning, portable, err := readLearningSnapshot(path, device)
		if err != nil {
			diagnostic := LearningSnapshotDiagnostic{Device: device, Path: path, Kind: "malformed", Message: err.Error()}
			result.Diagnostics = append(result.Diagnostics, diagnostic)
			if report != nil {
				report(diagnostic)
			}
			continue
		}
		result.Learning = inputmigration.MergeLearning(result.Learning, learning)
		result.Portable = MergePortableLearningSnapshots(result.Portable, portable)
	}
	return result, nil
}

// ReadMergedLearning is a compact convenience wrapper for callers interested
// only in the merged SummaryOnly learning summary and pending device names.
func ReadMergedLearning(dir string, knownDevices []string, report func(LearningSnapshotDiagnostic)) (inputmigration.LearningData, []string, error) {
	result, err := ReadMergedLearningSnapshots(dir, knownDevices, report)
	return result.Learning, result.PendingDevices, err
}

// MergePortableLearningSnapshots deterministically merges two non-hashed
// portable learning snapshots using the same associative, non-inflating
// frequency rules as inputmigration.MergeLearning.
func MergePortableLearningSnapshots(a, b PortableLearningSnapshot) PortableLearningSnapshot {
	deviceSet := make(map[string]struct{}, len(a.Devices)+len(b.Devices))
	for _, d := range a.Devices {
		if d = strings.TrimSpace(d); d != "" {
			deviceSet[d] = struct{}{}
		}
	}
	for _, d := range b.Devices {
		if d = strings.TrimSpace(d); d != "" {
			deviceSet[d] = struct{}{}
		}
	}
	devices := make([]string, 0, len(deviceSet))
	for d := range deviceSet {
		devices = append(devices, d)
	}
	sort.Strings(devices)

	return PortableLearningSnapshot{
		Devices: devices,
		Words:   mergePortableWords(a.Words, b.Words),
		Bigrams: mergePortableBigrams(a.Bigrams, b.Bigrams),
	}
}

// HydrateMergedLearning imports a non-hashed/portable merged learning snapshot
// into s idempotently. Re-running with the same snapshot preserves pinned words
// and keeps MAX(existing, snapshot) frequency/recency without double-counting.
func (s *LearningStore) HydrateMergedLearning(ctx context.Context, snapshot PortableLearningSnapshot) error {
	if s == nil || s.db == nil {
		return errors.New("nil learning store")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	normalized := MergePortableLearningSnapshots(PortableLearningSnapshot{}, snapshot)
	type cleanWord struct {
		pinyin   string
		word     string
		freq     int64
		pinned   bool
		lastUsed int64
	}
	type cleanBigram struct {
		prevWord string
		word     string
		freq     int64
		lastUsed int64
	}

	words := make([]cleanWord, 0, len(normalized.Words))
	for _, w := range normalized.Words {
		py := truncateUTF8(strings.TrimSpace(w.PinyinKey), MaxMirrorKeyBytes)
		word := truncateUTF8(w.Word, MaxMirrorKeyBytes)
		if py == "" || word == "" || (w.Frequency == 0 && !w.Pinned) {
			continue
		}
		words = append(words, cleanWord{
			pinyin:   py,
			word:     word,
			freq:     clampUint64ToInt64(w.Frequency),
			pinned:   w.Pinned,
			lastUsed: w.LastUsedNS,
		})
	}
	bigrams := make([]cleanBigram, 0, len(normalized.Bigrams))
	for _, b := range normalized.Bigrams {
		prev := truncateUTF8(b.PrevWord, MaxMirrorKeyBytes)
		word := truncateUTF8(b.Word, MaxMirrorKeyBytes)
		if prev == "" || word == "" || b.Frequency == 0 {
			continue
		}
		bigrams = append(bigrams, cleanBigram{
			prevWord: prev,
			word:     word,
			freq:     clampUint64ToInt64(b.Frequency),
			lastUsed: b.LastUsedNS,
		})
	}
	if len(words) == 0 && len(bigrams) == 0 {
		return nil
	}

	stmts := make([]sqlite.Statement, 0, len(words)+len(bigrams))
	for _, w := range words {
		stmts = append(stmts, sqlite.Statement{
			SQL: `INSERT INTO ime_user_lexicon(pinyin_key,word,frequency,last_used_ns,pinned)
				VALUES(?,?,?,?,?)
				ON CONFLICT(pinyin_key,word) DO UPDATE SET
					frequency=MAX(frequency,excluded.frequency),
					last_used_ns=MAX(last_used_ns,excluded.last_used_ns),
					pinned=MAX(pinned,excluded.pinned)`,
			Args: []any{w.pinyin, w.word, w.freq, w.lastUsed, boolInt(w.pinned)},
		})
	}
	for _, b := range bigrams {
		stmts = append(stmts, sqlite.Statement{
			SQL: `INSERT INTO ime_bigrams(prev_word,word,transition_count,last_used_ns)
				VALUES(?,?,?,?)
				ON CONFLICT(prev_word,word) DO UPDATE SET
					transition_count=MAX(transition_count,excluded.transition_count),
					last_used_ns=MAX(last_used_ns,excluded.last_used_ns)`,
			Args: []any{b.prevWord, b.word, b.freq, b.lastUsed},
		})
	}
	if _, err := s.db.Transaction(ctx, stmts); err != nil {
		return fmt.Errorf("hydrate merged learning: %w", err)
	}

	s.mu.Lock()
	for _, w := range words {
		k := lexKey{w.pinyin, w.word, ""}
		cur, exists := s.lex[k]
		if !exists {
			if len(s.lex) >= MaxLexMirrorEntries {
				s.evictLexEntriesLocked(MaxLexMirrorEntries)
			}
			s.indexLexKeyLocked(k)
		}
		if w.freq > cur.frequency {
			cur.frequency = w.freq
		}
		if w.lastUsed > cur.lastUsed {
			cur.lastUsed = w.lastUsed
		}
		cur.pinned = cur.pinned || w.pinned
		s.lex[k] = cur
	}
	for _, b := range bigrams {
		bk := [2]string{b.prevWord, b.word}
		cur, exists := s.bigrams[bk]
		if !exists && len(s.bigrams) >= MaxBigramMirrorEntries {
			evictBigramEntries(s.bigrams, MaxBigramMirrorEntries)
		}
		if b.freq > cur.frequency {
			cur.frequency = b.freq
		}
		if b.lastUsed > cur.lastUsed {
			cur.lastUsed = b.lastUsed
		}
		s.bigrams[bk] = cur
	}
	s.mu.Unlock()
	s.bigramGen.Add(1)
	s.flushWatermark.Add(1)
	return nil
}

// HydrateMergedLearning is the package-level spelling for store.HydrateMergedLearning.
func HydrateMergedLearning(ctx context.Context, store *LearningStore, snapshot PortableLearningSnapshot) error {
	return store.HydrateMergedLearning(ctx, snapshot)
}

func (s *LearningStore) snapshotLines() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var words []snapshotWord
	for key, value := range s.lex {
		if key.prev == "" && key.pinyin != "" && key.word != "" {
			words = append(words, snapshotWord{pinyin: key.pinyin, word: key.word, frequency: value.frequency, pinned: value.pinned, lastUsed: value.lastUsed})
		}
	}
	sort.Slice(words, func(i, j int) bool {
		if words[i].pinyin != words[j].pinyin {
			return words[i].pinyin < words[j].pinyin
		}
		return words[i].word < words[j].word
	})
	lines := []string{"# clavis-userdb-v1"}
	for _, word := range words {
		lines = append(lines, word.word+"\t"+word.pinyin+"\t"+strconv.FormatInt(word.frequency, 10))
		if word.pinned || word.lastUsed != 0 {
			lines = append(lines, fmt.Sprintf("# clavis-pinned\t%s\t%s\t%d\t%d", word.pinyin, word.word, boolInt(word.pinned), word.lastUsed))
		}
	}
	var bigrams []snapshotBigram
	for key, value := range s.bigrams {
		if key[0] != "" && key[1] != "" && value.frequency > 0 {
			bigrams = append(bigrams, snapshotBigram{previous: key[0], word: key[1], frequency: value.frequency, lastUsed: value.lastUsed})
		}
	}
	sort.Slice(bigrams, func(i, j int) bool {
		if bigrams[i].previous != bigrams[j].previous {
			return bigrams[i].previous < bigrams[j].previous
		}
		return bigrams[i].word < bigrams[j].word
	})
	for _, bigram := range bigrams {
		lines = append(lines, fmt.Sprintf("# clavis-bigram\t%s\t%s\t%d\t%d", bigram.previous, bigram.word, bigram.frequency, bigram.lastUsed))
	}
	return lines
}

type snapshotWord struct {
	pinyin, word string
	frequency    int64
	pinned       bool
	lastUsed     int64
}
type snapshotBigram struct {
	previous, word string
	frequency      int64
	lastUsed       int64
}

func readLearningSnapshot(path, device string) (inputmigration.LearningData, PortableLearningSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return inputmigration.LearningData{}, PortableLearningSnapshot{}, err
	}
	defer f.Close()
	type rawWord struct {
		pinyin, word string
		frequency    uint64
	}
	var rawWords []rawWord
	var bigrams []inputmigration.BigramEntry
	var portableBigrams []PortableBigramEntry
	pinned := make(map[[2]string]bool)
	lastUsed := make(map[[2]string]int64)
	var malformed error
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "# clavis-pinned\t") {
			fields := strings.Split(line, "\t")
			if len(fields) != 5 {
				malformed = fmt.Errorf("invalid pinned metadata in %s", path)
				break
			}
			flag, flagErr := strconv.ParseBool(fields[3])
			ns, nsErr := strconv.ParseInt(fields[4], 10, 64)
			if flagErr != nil || nsErr != nil {
				malformed = fmt.Errorf("invalid pinned metadata in %s", path)
				break
			}
			key := [2]string{fields[1], fields[2]}
			pinned[key] = flag
			lastUsed[key] = ns
			continue
		}
		if strings.HasPrefix(line, "# clavis-bigram\t") {
			fields := strings.Split(line, "\t")
			if len(fields) != 5 {
				malformed = fmt.Errorf("invalid bigram metadata in %s", path)
				break
			}
			frequency, freqErr := strconv.ParseUint(fields[3], 10, 64)
			ns, nsErr := strconv.ParseInt(fields[4], 10, 64)
			if freqErr != nil || nsErr != nil || fields[1] == "" || fields[2] == "" {
				malformed = fmt.Errorf("invalid bigram metadata in %s", path)
				break
			}
			entry := inputmigration.NewBigramEntry(fields[1], fields[2], frequency)
			entry.LastUsedNS = ns
			bigrams = append(bigrams, entry)
			portableBigrams = append(portableBigrams, PortableBigramEntry{
				PrevWord:   fields[1],
				Word:       fields[2],
				Frequency:  frequency,
				LastUsedNS: ns,
			})
			continue
		}
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] == "" || fields[1] == "" {
			malformed = fmt.Errorf("invalid userdb record in %s", path)
			break
		}
		frequency, freqErr := strconv.ParseUint(fields[2], 10, 64)
		if freqErr != nil {
			malformed = fmt.Errorf("invalid userdb weight in %s", path)
			break
		}
		rawWords = append(rawWords, rawWord{pinyin: fields[1], word: fields[0], frequency: frequency})
	}
	if err := scanner.Err(); err != nil {
		return inputmigration.LearningData{}, PortableLearningSnapshot{}, err
	}
	if malformed != nil {
		return inputmigration.LearningData{}, PortableLearningSnapshot{}, malformed
	}
	words := make([]inputmigration.LearningEntry, 0, len(rawWords))
	portableWords := make([]PortableWordEntry, 0, len(rawWords))
	for _, raw := range rawWords {
		key := [2]string{raw.pinyin, raw.word}
		entry := inputmigration.NewLearningEntry(raw.pinyin, raw.word, raw.frequency, pinned[key])
		entry.LastUsedNS = lastUsed[key]
		words = append(words, entry)
		portableWords = append(portableWords, PortableWordEntry{
			PinyinKey:  raw.pinyin,
			Word:       raw.word,
			Frequency:  raw.frequency,
			Pinned:     pinned[key],
			LastUsedNS: lastUsed[key],
		})
	}
	summary := inputmigration.LearningData{
		Enabled:     len(words) > 0 || len(bigrams) > 0,
		SummaryOnly: true,
		Words:       words,
		Bigrams:     bigrams,
	}
	portable := PortableLearningSnapshot{
		Devices: []string{device},
		Words:   portableWords,
		Bigrams: portableBigrams,
	}
	return summary, portable, nil
}

func mergePortableWords(a, b []PortableWordEntry) []PortableWordEntry {
	merged := make(map[[2]string]PortableWordEntry, len(a)+len(b))
	for _, entry := range append(append([]PortableWordEntry(nil), a...), b...) {
		if entry.PinyinKey == "" || entry.Word == "" {
			continue
		}
		key := [2]string{entry.PinyinKey, entry.Word}
		cur, exists := merged[key]
		if !exists {
			cur = PortableWordEntry{PinyinKey: entry.PinyinKey, Word: entry.Word}
		}
		cur.Frequency = addPortableFrequency(cur.Frequency, entry.Frequency)
		cur.Pinned = cur.Pinned || entry.Pinned
		if entry.LastUsedNS > cur.LastUsedNS {
			cur.LastUsedNS = entry.LastUsedNS
		}
		merged[key] = cur
	}

	out := make([]PortableWordEntry, 0, len(merged))
	for _, entry := range merged {
		if !entry.Pinned && entry.Frequency > maxPortableFrequency {
			entry.Frequency = maxPortableFrequency
		}
		out = append(out, entry)
	}
	out = retainTopPortableWords(out)
	sort.Slice(out, func(i, j int) bool {
		if out[i].PinyinKey != out[j].PinyinKey {
			return out[i].PinyinKey < out[j].PinyinKey
		}
		return out[i].Word < out[j].Word
	})
	return out
}

func retainTopPortableWords(entries []PortableWordEntry) []PortableWordEntry {
	if len(entries) <= maxPortableEntries {
		return entries
	}
	pinned := 0
	for _, e := range entries {
		if e.Pinned {
			pinned++
		}
	}
	if pinned >= maxPortableEntries {
		kept := make([]PortableWordEntry, 0, pinned)
		for _, e := range entries {
			if e.Pinned {
				kept = append(kept, e)
			}
		}
		return kept
	}
	keepUnpinned := maxPortableEntries - pinned
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Pinned != entries[j].Pinned {
			return entries[i].Pinned
		}
		if entries[i].Frequency != entries[j].Frequency {
			return entries[i].Frequency > entries[j].Frequency
		}
		if entries[i].LastUsedNS != entries[j].LastUsedNS {
			return entries[i].LastUsedNS > entries[j].LastUsedNS
		}
		if entries[i].PinyinKey != entries[j].PinyinKey {
			return entries[i].PinyinKey < entries[j].PinyinKey
		}
		return entries[i].Word < entries[j].Word
	})
	kept := make([]PortableWordEntry, 0, maxPortableEntries)
	for _, e := range entries {
		if e.Pinned || keepUnpinned > 0 {
			kept = append(kept, e)
			if !e.Pinned {
				keepUnpinned--
			}
		}
	}
	return kept
}

func mergePortableBigrams(a, b []PortableBigramEntry) []PortableBigramEntry {
	merged := make(map[[2]string]PortableBigramEntry, len(a)+len(b))
	for _, entry := range append(append([]PortableBigramEntry(nil), a...), b...) {
		if entry.PrevWord == "" || entry.Word == "" {
			continue
		}
		key := [2]string{entry.PrevWord, entry.Word}
		cur, exists := merged[key]
		if !exists {
			cur = PortableBigramEntry{PrevWord: entry.PrevWord, Word: entry.Word}
		}
		cur.Frequency = addPortableFrequency(cur.Frequency, entry.Frequency)
		if entry.LastUsedNS > cur.LastUsedNS {
			cur.LastUsedNS = entry.LastUsedNS
		}
		merged[key] = cur
	}

	out := make([]PortableBigramEntry, 0, len(merged))
	for _, entry := range merged {
		if entry.Frequency > maxPortableFrequency {
			entry.Frequency = maxPortableFrequency
		}
		out = append(out, entry)
	}
	if len(out) > maxPortableEntries {
		sort.Slice(out, func(i, j int) bool {
			if out[i].Frequency != out[j].Frequency {
				return out[i].Frequency > out[j].Frequency
			}
			if out[i].LastUsedNS != out[j].LastUsedNS {
				return out[i].LastUsedNS > out[j].LastUsedNS
			}
			if out[i].PrevWord != out[j].PrevWord {
				return out[i].PrevWord < out[j].PrevWord
			}
			return out[i].Word < out[j].Word
		})
		out = out[:maxPortableEntries]
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PrevWord != out[j].PrevWord {
			return out[i].PrevWord < out[j].PrevWord
		}
		return out[i].Word < out[j].Word
	})
	return out
}

func addPortableFrequency(a, b uint64) uint64 {
	if ^uint64(0)-a < b {
		return ^uint64(0)
	}
	return a + b
}

func validateSnapshotDevice(device string) error {
	if device == "" || device == "." || device == ".." || strings.ContainsAny(device, `/\\`) || strings.Contains(device, "..") {
		return fmt.Errorf("invalid learning snapshot device %q", device)
	}
	return nil
}
