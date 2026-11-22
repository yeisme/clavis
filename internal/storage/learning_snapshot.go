package storage

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/yeisme/clavis/pkg/core/inputmigration"
)

const (
	// DefaultLearningSnapshotDevices is used when the caller has not configured
	// an explicit installation allow-list.
	DefaultLearningSnapshotDevices = "mini,windows"
	learningSnapshotSuffix         = ".userdb.txt"
)

// LearningSnapshotDiagnostic is emitted for a snapshot that needs operator
// attention. Unknown devices are deliberately diagnostics, not silent skips.
type LearningSnapshotDiagnostic struct {
	Device  string
	Path    string
	Kind    string
	Message string
}

// LearningSnapshotRead is the read-time result of merging the approved device
// files. PendingDevices contains files that were found but are not in the
// approved device list.
type LearningSnapshotRead struct {
	Learning       inputmigration.LearningData
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

// ReadMergedLearningSnapshots scans approved per-device files and merges their
// migration summaries with inputmigration.MergeLearning. Unknown files are
// returned in PendingDevices and reported through report; they are never
// silently incorporated or discarded.
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
			return LearningSnapshotRead{Learning: inputmigration.LearningData{SummaryOnly: false}}, nil
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
	result := LearningSnapshotRead{Learning: inputmigration.LearningData{SummaryOnly: false}}
	merged := false
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
		learning, err := readLearningSnapshot(path)
		if err != nil {
			diagnostic := LearningSnapshotDiagnostic{Device: device, Path: path, Kind: "malformed", Message: err.Error()}
			result.Diagnostics = append(result.Diagnostics, diagnostic)
			if report != nil {
				report(diagnostic)
			}
			continue
		}
		if !merged {
			result.Learning = learning
			merged = true
		} else {
			result.Learning = inputmigration.MergeLearning(result.Learning, learning)
		}
	}
	return result, nil
}

// ReadMergedLearning is a compact convenience wrapper for callers interested
// only in the merged learning and pending device names.
func ReadMergedLearning(dir string, knownDevices []string, report func(LearningSnapshotDiagnostic)) (inputmigration.LearningData, []string, error) {
	result, err := ReadMergedLearningSnapshots(dir, knownDevices, report)
	return result.Learning, result.PendingDevices, err
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

func readLearningSnapshot(path string) (inputmigration.LearningData, error) {
	f, err := os.Open(path)
	if err != nil {
		return inputmigration.LearningData{}, err
	}
	defer f.Close()
	type rawWord struct {
		pinyin, word string
		frequency    uint64
	}
	var rawWords []rawWord
	var bigrams []inputmigration.BigramEntry
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
		return inputmigration.LearningData{}, err
	}
	if malformed != nil {
		return inputmigration.LearningData{}, malformed
	}
	words := make([]inputmigration.LearningEntry, 0, len(rawWords))
	for _, raw := range rawWords {
		key := [2]string{raw.pinyin, raw.word}
		entry := inputmigration.NewLearningEntry(raw.pinyin, raw.word, raw.frequency, pinned[key])
		entry.LastUsedNS = lastUsed[key]
		words = append(words, entry)
	}
	return inputmigration.LearningData{Enabled: len(words) > 0 || len(bigrams) > 0, Words: words, Bigrams: bigrams}, nil
}

func validateSnapshotDevice(device string) error {
	if device == "" || device == "." || device == ".." || strings.ContainsAny(device, `/\\`) || strings.Contains(device, "..") {
		return fmt.Errorf("invalid learning snapshot device %q", device)
	}
	return nil
}
