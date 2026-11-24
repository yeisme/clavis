package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/plugins/sqlite"
)

const (
	// BigramSnapshotFilename is the canonical filename for the cold-start bigram binary cache.
	BigramSnapshotFilename = "bigram-snapshot-v1.bin"
	// BigramSnapshotFormatVersion is the supported wire format version of bigram-snapshot-v1.bin.
	BigramSnapshotFormatVersion uint16 = 1
	// MaxBigramSnapshotEntries matches MaxBigramMirrorEntries so cold-start mirrors stay bounded.
	MaxBigramSnapshotEntries = MaxBigramMirrorEntries
	// MaxBigramSnapshotBytes caps the total encoded size of bigram-snapshot-v1.bin (2 MiB).
	MaxBigramSnapshotBytes = 2 << 20

	bigramSnapshotHeaderBytes  = 8 + 2 + 2 + 4 + 8 + 8 + 8 + 32 + 4 // 76 bytes
	bigramSnapshotTrailerBytes = sha256.Size                        // 32 bytes
	bigramSnapshotFlagTrunc    = uint16(1 << 0)
)

var bigramSnapshotMagic = [8]byte{'C', 'L', 'V', 'B', 'G', '0', '1', 0}

var (
	// ErrBigramSnapshotInvalid indicates a malformed, truncated, or structurally invalid snapshot.
	ErrBigramSnapshotInvalid = errors.New("bigram_snapshot_invalid")
	// ErrBigramSnapshotVersion indicates an unsupported snapshot format version.
	ErrBigramSnapshotVersion = errors.New("bigram_snapshot_version")
	// ErrBigramSnapshotChecksum indicates the trailing SHA-256 checksum or source digest did not match.
	ErrBigramSnapshotChecksum = errors.New("bigram_snapshot_checksum")
	// ErrBigramSnapshotTooLarge indicates the snapshot file exceeds MaxBigramSnapshotBytes.
	ErrBigramSnapshotTooLarge = errors.New("bigram_snapshot_too_large")
	// ErrBigramSnapshotStale indicates the snapshot generation/schema does not match the backing DB.
	ErrBigramSnapshotStale = errors.New("bigram_snapshot_stale")
)

// BigramSnapshotEntry is one hot bigram transition cached for IME cold start.
type BigramSnapshotEntry struct {
	PrevWord   string
	Word       string
	Reading    string
	Frequency  uint64
	LastUsedNS int64
	UsageCount uint64
}

// BigramSnapshot is a disposable, checksummed, generation-stamped cold-start cache derived
// from ime_bigrams (+ reading/usage hints). SQLite remains the sole source of truth.
type BigramSnapshot struct {
	FormatVersion  uint16
	CreatedAt      time.Time
	SourceSchema   int
	Generation     uint64
	FlushWatermark uint64
	SourceDigest   [32]byte
	Truncated      bool
	Entries        []BigramSnapshotEntry
}

// BigramSnapshotStore persists and loads cold-start bigram snapshots.
type BigramSnapshotStore interface {
	Load(ctx context.Context) (BigramSnapshot, error)
	Publish(ctx context.Context, snapshot BigramSnapshot) error
}

// FileBigramSnapshotStore implements BigramSnapshotStore backed by a single binary file.
type FileBigramSnapshotStore struct {
	Path string
}

// Load reads and validates the binary snapshot at s.Path.
func (s FileBigramSnapshotStore) Load(ctx context.Context) (BigramSnapshot, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return BigramSnapshot{}, err
		}
	}
	return ReadBigramSnapshot(s.Path)
}

// Publish atomically writes snapshot to s.Path.
func (s FileBigramSnapshotStore) Publish(ctx context.Context, snapshot BigramSnapshot) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return WriteBigramSnapshot(s.Path, snapshot)
}

// EncodeBigramSnapshot canonicalizes, bounds, checksums, and encodes snap into the v1 binary format.
// Identical normalized inputs produce byte-for-byte identical output.
func EncodeBigramSnapshot(snap BigramSnapshot) ([]byte, error) {
	normalized := normalizeBigramSnapshot(snap)

	var buf bytes.Buffer
	// Reserve header; we fill RecordCount, Flags, and SourceDigest after bounding entries.
	header := make([]byte, bigramSnapshotHeaderBytes)
	buf.Write(header)

	kept := make([]BigramSnapshotEntry, 0, len(normalized.Entries))
	truncated := normalized.Truncated

	for _, entry := range normalized.Entries {
		if len(kept) >= MaxBigramSnapshotEntries {
			truncated = true
			break
		}
		entrySize := 2 + len(entry.PrevWord) + 2 + len(entry.Word) + 2 + len(entry.Reading) + 8 + 8 + 8
		if buf.Len()+entrySize+bigramSnapshotTrailerBytes > MaxBigramSnapshotBytes {
			truncated = true
			break
		}
		if err := writeSnapshotEntry(&buf, entry); err != nil {
			return nil, err
		}
		kept = append(kept, entry)
	}

	normalized.Entries = kept
	normalized.Truncated = truncated
	normalized.SourceDigest = computeBigramSourceDigest(normalized.SourceSchema, normalized.Generation, normalized.FlushWatermark, normalized.Entries)

	raw := buf.Bytes()
	copy(raw[0:8], bigramSnapshotMagic[:])
	binary.LittleEndian.PutUint16(raw[8:10], BigramSnapshotFormatVersion)
	var flags uint16
	if normalized.Truncated {
		flags |= bigramSnapshotFlagTrunc
	}
	binary.LittleEndian.PutUint16(raw[10:12], flags)
	binary.LittleEndian.PutUint32(raw[12:16], uint32(max(normalized.SourceSchema, 0)))
	var createdNS int64
	if !normalized.CreatedAt.IsZero() {
		createdNS = normalized.CreatedAt.UTC().UnixNano()
	}
	binary.LittleEndian.PutUint64(raw[16:24], uint64(createdNS))
	binary.LittleEndian.PutUint64(raw[24:32], normalized.Generation)
	binary.LittleEndian.PutUint64(raw[32:40], normalized.FlushWatermark)
	copy(raw[40:72], normalized.SourceDigest[:])
	binary.LittleEndian.PutUint32(raw[72:76], uint32(len(normalized.Entries)))

	sum := sha256.Sum256(raw)
	out := make([]byte, len(raw)+bigramSnapshotTrailerBytes)
	copy(out, raw)
	copy(out[len(raw):], sum[:])
	return out, nil
}

// DecodeBigramSnapshot validates magic, version, trailing SHA-256 checksum, entry bounds,
// and canonical SourceDigest, returning a clean typed error on any corruption.
func DecodeBigramSnapshot(data []byte) (BigramSnapshot, error) {
	if len(data) > MaxBigramSnapshotBytes {
		return BigramSnapshot{}, fmt.Errorf("%w: size %d exceeds %d", ErrBigramSnapshotTooLarge, len(data), MaxBigramSnapshotBytes)
	}
	if len(data) < bigramSnapshotHeaderBytes+bigramSnapshotTrailerBytes {
		return BigramSnapshot{}, fmt.Errorf("%w: truncated snapshot (%d bytes)", ErrBigramSnapshotInvalid, len(data))
	}
	if !bytes.Equal(data[0:8], bigramSnapshotMagic[:]) {
		return BigramSnapshot{}, fmt.Errorf("%w: bad magic header", ErrBigramSnapshotInvalid)
	}
	version := binary.LittleEndian.Uint16(data[8:10])
	if version != BigramSnapshotFormatVersion {
		return BigramSnapshot{}, fmt.Errorf("%w: unsupported version %d", ErrBigramSnapshotVersion, version)
	}

	payloadEnd := len(data) - bigramSnapshotTrailerBytes
	wantSum := sha256.Sum256(data[:payloadEnd])
	if !bytes.Equal(wantSum[:], data[payloadEnd:]) {
		return BigramSnapshot{}, fmt.Errorf("%w: trailing SHA-256 mismatch", ErrBigramSnapshotChecksum)
	}

	flags := binary.LittleEndian.Uint16(data[10:12])
	if flags&^bigramSnapshotFlagTrunc != 0 {
		return BigramSnapshot{}, fmt.Errorf("%w: unknown header flags 0x%x", ErrBigramSnapshotInvalid, flags)
	}
	sourceSchema := int(binary.LittleEndian.Uint32(data[12:16]))
	createdNS := int64(binary.LittleEndian.Uint64(data[16:24]))
	generation := binary.LittleEndian.Uint64(data[24:32])
	flushWatermark := binary.LittleEndian.Uint64(data[32:40])
	var sourceDigest [32]byte
	copy(sourceDigest[:], data[40:72])
	recordCount := binary.LittleEndian.Uint32(data[72:76])
	if int(recordCount) > MaxBigramSnapshotEntries {
		return BigramSnapshot{}, fmt.Errorf("%w: record count %d exceeds cap %d", ErrBigramSnapshotInvalid, recordCount, MaxBigramSnapshotEntries)
	}

	entries := make([]BigramSnapshotEntry, 0, recordCount)
	offset := bigramSnapshotHeaderBytes
	for i := uint32(0); i < recordCount; i++ {
		entry, nextOffset, err := readSnapshotEntry(data[:payloadEnd], offset)
		if err != nil {
			return BigramSnapshot{}, err
		}
		entries = append(entries, entry)
		offset = nextOffset
	}
	if offset != payloadEnd {
		return BigramSnapshot{}, fmt.Errorf("%w: trailing unconsumed bytes (%d)", ErrBigramSnapshotInvalid, payloadEnd-offset)
	}

	expectedDigest := computeBigramSourceDigest(sourceSchema, generation, flushWatermark, entries)
	if sourceDigest != expectedDigest {
		return BigramSnapshot{}, fmt.Errorf("%w: source digest mismatch", ErrBigramSnapshotChecksum)
	}

	var createdAt time.Time
	if createdNS != 0 {
		createdAt = time.Unix(0, createdNS).UTC()
	}
	return BigramSnapshot{
		FormatVersion:  version,
		CreatedAt:      createdAt,
		SourceSchema:   sourceSchema,
		Generation:     generation,
		FlushWatermark: flushWatermark,
		SourceDigest:   sourceDigest,
		Truncated:      flags&bigramSnapshotFlagTrunc != 0,
		Entries:        entries,
	}, nil
}

// WriteBigramSnapshot atomically writes snap to path using temp file + chmod 0600 + fsync + rename.
func WriteBigramSnapshot(path string, snap BigramSnapshot) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: empty snapshot path", ErrBigramSnapshotInvalid)
	}
	encoded, err := EncodeBigramSnapshot(snap)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, encoded, 0o600)
}

// ReadBigramSnapshot reads and strictly validates a binary bigram snapshot from path.
func ReadBigramSnapshot(path string) (BigramSnapshot, error) {
	if strings.TrimSpace(path) == "" {
		return BigramSnapshot{}, fmt.Errorf("%w: empty snapshot path", ErrBigramSnapshotInvalid)
	}
	f, err := os.Open(path)
	if err != nil {
		return BigramSnapshot{}, err
	}
	defer f.Close()

	if info, statErr := f.Stat(); statErr == nil {
		if info.IsDir() {
			return BigramSnapshot{}, fmt.Errorf("%w: snapshot path is a directory", ErrBigramSnapshotInvalid)
		}
		if info.Size() > MaxBigramSnapshotBytes {
			return BigramSnapshot{}, fmt.Errorf("%w: file size %d exceeds %d", ErrBigramSnapshotTooLarge, info.Size(), MaxBigramSnapshotBytes)
		}
	}

	data, err := io.ReadAll(io.LimitReader(f, MaxBigramSnapshotBytes+1))
	if err != nil {
		return BigramSnapshot{}, err
	}
	if len(data) > MaxBigramSnapshotBytes {
		return BigramSnapshot{}, fmt.Errorf("%w: snapshot exceeds %d bytes", ErrBigramSnapshotTooLarge, MaxBigramSnapshotBytes)
	}
	return DecodeBigramSnapshot(data)
}

func normalizeBigramSnapshot(snap BigramSnapshot) BigramSnapshot {
	// Deduplicate by (PrevWord, Word) keeping the entry with higher frequency/recency/reading.
	byPair := make(map[[2]string]BigramSnapshotEntry, len(snap.Entries))
	for _, raw := range snap.Entries {
		prev := truncateUTF8(strings.TrimSpace(raw.PrevWord), MaxMirrorKeyBytes)
		word := truncateUTF8(strings.TrimSpace(raw.Word), MaxMirrorKeyBytes)
		reading := truncateUTF8(strings.TrimSpace(raw.Reading), MaxMirrorKeyBytes)
		if prev == "" || word == "" || raw.Frequency == 0 {
			continue
		}
		e := BigramSnapshotEntry{
			PrevWord:   prev,
			Word:       word,
			Reading:    reading,
			Frequency:  raw.Frequency,
			LastUsedNS: raw.LastUsedNS,
			UsageCount: raw.UsageCount,
		}
		key := [2]string{prev, word}
		if cur, ok := byPair[key]; ok {
			if e.Frequency > cur.Frequency || (e.Frequency == cur.Frequency && e.LastUsedNS > cur.LastUsedNS) {
				if e.Reading == "" {
					e.Reading = cur.Reading
				}
				if e.UsageCount < cur.UsageCount {
					e.UsageCount = cur.UsageCount
				}
				byPair[key] = e
			} else {
				if cur.Reading == "" && e.Reading != "" {
					cur.Reading = e.Reading
				}
				if e.UsageCount > cur.UsageCount {
					cur.UsageCount = e.UsageCount
				}
				byPair[key] = cur
			}
		} else {
			byPair[key] = e
		}
	}

	entries := make([]BigramSnapshotEntry, 0, len(byPair))
	for _, e := range byPair {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Frequency != entries[j].Frequency {
			return entries[i].Frequency > entries[j].Frequency
		}
		if entries[i].LastUsedNS != entries[j].LastUsedNS {
			return entries[i].LastUsedNS > entries[j].LastUsedNS
		}
		if entries[i].PrevWord != entries[j].PrevWord {
			return entries[i].PrevWord < entries[j].PrevWord
		}
		if entries[i].Word != entries[j].Word {
			return entries[i].Word < entries[j].Word
		}
		return entries[i].Reading < entries[j].Reading
	})

	snap.FormatVersion = BigramSnapshotFormatVersion
	snap.Entries = entries
	return snap
}

func computeBigramSourceDigest(sourceSchema int, generation, flushWatermark uint64, entries []BigramSnapshotEntry) [32]byte {
	h := sha256.New()
	var num [8]byte
	binary.LittleEndian.PutUint32(num[:4], uint32(max(sourceSchema, 0)))
	_, _ = h.Write(num[:4])
	binary.LittleEndian.PutUint64(num[:], generation)
	_, _ = h.Write(num[:])
	binary.LittleEndian.PutUint64(num[:], flushWatermark)
	_, _ = h.Write(num[:])
	binary.LittleEndian.PutUint32(num[:4], uint32(len(entries)))
	_, _ = h.Write(num[:4])
	for _, e := range entries {
		binary.LittleEndian.PutUint16(num[:2], uint16(len(e.PrevWord)))
		_, _ = h.Write(num[:2])
		_, _ = io.WriteString(h, e.PrevWord)
		binary.LittleEndian.PutUint16(num[:2], uint16(len(e.Word)))
		_, _ = h.Write(num[:2])
		_, _ = io.WriteString(h, e.Word)
		binary.LittleEndian.PutUint16(num[:2], uint16(len(e.Reading)))
		_, _ = h.Write(num[:2])
		_, _ = io.WriteString(h, e.Reading)
		binary.LittleEndian.PutUint64(num[:], e.Frequency)
		_, _ = h.Write(num[:])
		binary.LittleEndian.PutUint64(num[:], uint64(e.LastUsedNS))
		_, _ = h.Write(num[:])
		binary.LittleEndian.PutUint64(num[:], e.UsageCount)
		_, _ = h.Write(num[:])
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func writeSnapshotEntry(buf *bytes.Buffer, e BigramSnapshotEntry) error {
	if len(e.PrevWord) > MaxMirrorKeyBytes || len(e.Word) > MaxMirrorKeyBytes || len(e.Reading) > MaxMirrorKeyBytes {
		return fmt.Errorf("%w: entry string exceeds max length", ErrBigramSnapshotInvalid)
	}
	var tmp [8]byte
	writeStr := func(s string) {
		binary.LittleEndian.PutUint16(tmp[:2], uint16(len(s)))
		buf.Write(tmp[:2])
		buf.WriteString(s)
	}
	writeStr(e.PrevWord)
	writeStr(e.Word)
	writeStr(e.Reading)
	binary.LittleEndian.PutUint64(tmp[:], e.Frequency)
	buf.Write(tmp[:])
	binary.LittleEndian.PutUint64(tmp[:], uint64(e.LastUsedNS))
	buf.Write(tmp[:])
	binary.LittleEndian.PutUint64(tmp[:], e.UsageCount)
	buf.Write(tmp[:])
	return nil
}

func readSnapshotEntry(payload []byte, offset int) (BigramSnapshotEntry, int, error) {
	readStr := func(required bool) (string, error) {
		if offset+2 > len(payload) {
			return "", fmt.Errorf("%w: truncated string length", ErrBigramSnapshotInvalid)
		}
		n := int(binary.LittleEndian.Uint16(payload[offset : offset+2]))
		offset += 2
		if n > MaxMirrorKeyBytes {
			return "", fmt.Errorf("%w: string length %d exceeds %d", ErrBigramSnapshotInvalid, n, MaxMirrorKeyBytes)
		}
		if offset+n > len(payload) {
			return "", fmt.Errorf("%w: truncated string payload", ErrBigramSnapshotInvalid)
		}
		raw := payload[offset : offset+n]
		offset += n
		if !utf8.Valid(raw) {
			return "", fmt.Errorf("%w: invalid UTF-8 in snapshot entry", ErrBigramSnapshotInvalid)
		}
		s := string(raw)
		if required && s == "" {
			return "", fmt.Errorf("%w: empty required snapshot field", ErrBigramSnapshotInvalid)
		}
		return s, nil
	}
	prevWord, err := readStr(true)
	if err != nil {
		return BigramSnapshotEntry{}, 0, err
	}
	word, err := readStr(true)
	if err != nil {
		return BigramSnapshotEntry{}, 0, err
	}
	reading, err := readStr(false)
	if err != nil {
		return BigramSnapshotEntry{}, 0, err
	}
	if offset+24 > len(payload) {
		return BigramSnapshotEntry{}, 0, fmt.Errorf("%w: truncated entry counters", ErrBigramSnapshotInvalid)
	}
	frequency := binary.LittleEndian.Uint64(payload[offset : offset+8])
	lastUsedNS := int64(binary.LittleEndian.Uint64(payload[offset+8 : offset+16]))
	usageCount := binary.LittleEndian.Uint64(payload[offset+16 : offset+24])
	offset += 24
	if frequency == 0 {
		return BigramSnapshotEntry{}, 0, fmt.Errorf("%w: zero bigram frequency", ErrBigramSnapshotInvalid)
	}
	return BigramSnapshotEntry{
		PrevWord:   prevWord,
		Word:       word,
		Reading:    reading,
		Frequency:  frequency,
		LastUsedNS: lastUsedNS,
		UsageCount: usageCount,
	}, offset, nil
}

// dbBigramGeneration computes a fast, deterministic generation watermark over ime_bigrams
// using a single aggregate SQLite query (no full-table scan or string allocation).
func dbBigramGeneration(ctx context.Context, db *sqlite.DB, schemaVersion int) (uint64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := db.Query(ctx, `SELECT COUNT(*), COALESCE(SUM(transition_count), 0), COALESCE(MAX(last_used_ns), 0), COALESCE(SUM(length(prev_word) + length(word)), 0) FROM ime_bigrams`)
	if err != nil {
		return 0, err
	}
	var count, sumFreq, maxLastUsed, sumLens int64
	if len(rows.Values) > 0 && len(rows.Values[0]) >= 4 {
		count = int64val(rows.Values[0], 0)
		sumFreq = int64val(rows.Values[0], 1)
		maxLastUsed = int64val(rows.Values[0], 2)
		sumLens = int64val(rows.Values[0], 3)
	}
	h := fnv.New64a()
	var buf [8]byte
	for _, v := range []uint64{
		uint64(max(schemaVersion, 0)),
		uint64(count),
		uint64(sumFreq),
		uint64(maxLastUsed),
		uint64(sumLens),
	} {
		binary.LittleEndian.PutUint64(buf[:], v)
		_, _ = h.Write(buf[:])
	}
	gen := h.Sum64()
	if gen == 0 {
		gen = 1
	}
	return gen, nil
}
