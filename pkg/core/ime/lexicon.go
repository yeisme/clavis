// Package ime provides the platform-independent input method core.
package ime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Candidate struct {
	Text      string
	Word      string
	Frequency int32
	Score     int32
}

func (c Candidate) String() string {
	if c.Text != "" {
		return c.Text
	}
	return c.Word
}

type UserCandidate struct {
	Word      string
	Frequency int32
}
type UserFrequencyProvider interface {
	LookupBoost(pinyinKey, word, prevWord string) int32
	CustomWordsForPrefix(syllablePrefix string) []UserCandidate
}

type syllableEntry struct{ keyOff, keyLen, first, count uint32 }
type wordRecord struct {
	textOff, textLen, pyOff, pyLen uint32
	freq                           int32
}

// Lexicon is an immutable dictionary. The two indexes contain offsets only;
// strings and other pointers are kept exclusively in image.
type Lexicon struct {
	image     []byte
	syllables []syllableEntry
	words     []wordRecord
}

func (l *Lexicon) Len() int {
	if l == nil {
		return 0
	}
	return len(l.words)
}
func (l *Lexicon) Bytes() []byte {
	if l == nil {
		return nil
	}
	return l.image
}

const clvHeaderSize = 32

var clvMagic = [4]byte{'C', 'L', 'V', 'D'}

type lexItem struct {
	pinyin, word string
	freq         int32
}

func canonicalKey(s string) string { return strings.ReplaceAll(strings.ToLower(s), " ", "") }
func buildLexicon(items []lexItem) *Lexicon {
	sort.SliceStable(items, func(i, j int) bool {
		ki, kj := canonicalKey(items[i].pinyin), canonicalKey(items[j].pinyin)
		if ki == kj {
			return items[i].freq > items[j].freq
		}
		return ki < kj
	})
	// deduplicate exact key/word, preserving the strongest weight.
	out := items[:0]
	for _, it := range items {
		if len(out) > 0 && canonicalKey(out[len(out)-1].pinyin) == canonicalKey(it.pinyin) && out[len(out)-1].word == it.word {
			if it.freq > out[len(out)-1].freq {
				out[len(out)-1].freq = it.freq
			}
			continue
		}
		out = append(out, it)
	}
	items = out
	var keys []string
	for _, it := range items {
		k := canonicalKey(it.pinyin)
		if len(keys) == 0 || keys[len(keys)-1] != k {
			keys = append(keys, k)
		}
	}
	syOff := uint32(clvHeaderSize)
	wrOff := syOff + uint32(len(keys))*16
	strOff := wrOff + uint32(len(items))*20
	total := int(strOff)
	for _, k := range keys {
		total += len(k)
	}
	for _, it := range items {
		total += len(it.word) + len(it.pinyin)
	}
	image := make([]byte, total)
	copy(image[:4], clvMagic[:])
	binary.LittleEndian.PutUint16(image[4:], 1)
	binary.LittleEndian.PutUint32(image[8:], uint32(len(keys)))
	binary.LittleEndian.PutUint32(image[12:], uint32(len(items)))
	binary.LittleEndian.PutUint32(image[16:], syOff)
	binary.LittleEndian.PutUint32(image[20:], wrOff)
	binary.LittleEndian.PutUint32(image[24:], strOff)
	binary.LittleEndian.PutUint32(image[28:], uint32(total))
	syll := make([]syllableEntry, len(keys))
	words := make([]wordRecord, len(items))
	cursor := strOff
	wi := 0
	for i, k := range keys {
		ko := cursor
		copy(image[cursor:], k)
		cursor += uint32(len(k))
		first := wi
		for wi < len(items) && canonicalKey(items[wi].pinyin) == k {
			w := items[wi]
			to := cursor
			copy(image[cursor:], w.word)
			cursor += uint32(len(w.word))
			po := cursor
			copy(image[cursor:], w.pinyin)
			cursor += uint32(len(w.pinyin))
			words[wi] = wordRecord{to, uint32(len(w.word)), po, uint32(len(w.pinyin)), w.freq}
			wi++
		}
		syll[i] = syllableEntry{ko, uint32(len(k)), uint32(first), uint32(wi - first)}
		base := int(syOff) + i*16
		binary.LittleEndian.PutUint32(image[base:], ko)
		binary.LittleEndian.PutUint32(image[base+4:], uint32(len(k)))
		binary.LittleEndian.PutUint32(image[base+8:], uint32(first))
		binary.LittleEndian.PutUint32(image[base+12:], uint32(wi-first))
	}
	for i, w := range words {
		base := int(wrOff) + i*20
		binary.LittleEndian.PutUint32(image[base:], w.textOff)
		binary.LittleEndian.PutUint32(image[base+4:], w.textLen)
		binary.LittleEndian.PutUint32(image[base+8:], w.pyOff)
		binary.LittleEndian.PutUint32(image[base+12:], w.pyLen)
		binary.LittleEndian.PutUint32(image[base+16:], uint32(w.freq))
	}
	return &Lexicon{image: image, syllables: syll, words: words}
}

func LoadClvDict(data []byte) (*Lexicon, error) {
	if len(data) < clvHeaderSize || !bytes.Equal(data[:4], clvMagic[:]) {
		return nil, errors.New("ime: invalid CLVD header")
	}
	if binary.LittleEndian.Uint16(data[4:]) != 1 {
		return nil, errors.New("ime: unsupported CLVD version")
	}
	ns, nw := binary.LittleEndian.Uint32(data[8:]), binary.LittleEndian.Uint32(data[12:])
	so, wo := binary.LittleEndian.Uint32(data[16:]), binary.LittleEndian.Uint32(data[20:])
	total := binary.LittleEndian.Uint32(data[28:])
	if total > uint32(len(data)) || so+ns*16 > uint32(len(data)) || wo+nw*20 > uint32(len(data)) {
		return nil, errors.New("ime: truncated CLVD")
	}
	image := append([]byte(nil), data[:total]...)
	syll := make([]syllableEntry, ns)
	words := make([]wordRecord, nw)
	for i := range syll {
		b := int(so) + i*16
		syll[i] = syllableEntry{binary.LittleEndian.Uint32(image[b:]), binary.LittleEndian.Uint32(image[b+4:]), binary.LittleEndian.Uint32(image[b+8:]), binary.LittleEndian.Uint32(image[b+12:])}
		if int(syll[i].keyOff+syll[i].keyLen) > len(image) || syll[i].first+syll[i].count > nw {
			return nil, errors.New("ime: invalid CLVD index")
		}
	}
	for i := range words {
		b := int(wo) + i*20
		words[i] = wordRecord{binary.LittleEndian.Uint32(image[b:]), binary.LittleEndian.Uint32(image[b+4:]), binary.LittleEndian.Uint32(image[b+8:]), binary.LittleEndian.Uint32(image[b+12:]), int32(binary.LittleEndian.Uint32(image[b+16:]))}
		if int(words[i].textOff+words[i].textLen) > len(image) || int(words[i].pyOff+words[i].pyLen) > len(image) {
			return nil, errors.New("ime: invalid CLVD record")
		}
	}
	return &Lexicon{image: image, syllables: syll, words: words}, nil
}
func (l *Lexicon) key(i syllableEntry) string { return string(l.image[i.keyOff : i.keyOff+i.keyLen]) }
func (l *Lexicon) text(w wordRecord) string   { return string(l.image[w.textOff : w.textOff+w.textLen]) }
func (l *Lexicon) pinyin(w wordRecord) string { return string(l.image[w.pyOff : w.pyOff+w.pyLen]) }

// ForEachWord visits every record in l in canonical key / descending frequency order, stopping
// early when fn returns false.
func (l *Lexicon) ForEachWord(fn func(pinyin, word string, freq int32) bool) {
	if l == nil || fn == nil {
		return
	}
	for _, w := range l.words {
		if !fn(l.pinyin(w), l.text(w), w.freq) {
			return
		}
	}
}
func (l *Lexicon) lookupExact(key string) []Candidate {
	i := sort.Search(len(l.syllables), func(i int) bool { return l.key(l.syllables[i]) >= key })
	if i == len(l.syllables) || l.key(l.syllables[i]) != key {
		return nil
	}
	e := l.syllables[i]
	out := make([]Candidate, 0, e.count)
	for j := e.first; j < e.first+e.count; j++ {
		w := l.words[j]
		out = append(out, Candidate{Text: l.text(w), Word: l.text(w), Frequency: w.freq, Score: w.freq})
	}
	return out
}
func initials(py string) string {
	var b strings.Builder
	for _, s := range strings.Fields(py) {
		if s != "" {
			b.WriteByte(s[0])
		}
	}
	return b.String()
}
func (l *Lexicon) lookupInitial(key string) []Candidate {
	key = strings.ReplaceAll(strings.ToLower(key), " ", "")
	var out []Candidate
	for _, w := range l.words {
		if initials(l.pinyin(w)) == key {
			t := l.text(w)
			out = append(out, Candidate{Text: t, Word: t, Frequency: w.freq, Score: w.freq})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}
func (l *Lexicon) Lookup(key string) []Candidate {
	if l == nil {
		return nil
	}
	if x := l.lookupExact(canonicalKey(key)); len(x) > 0 {
		return x
	}
	return l.lookupInitial(strings.ToLower(key))
}

// compose returns a bounded best-path sentence for a sequence of syllables.
// It intentionally keeps the hot path simple: dictionaries remain immutable and
// only candidate values are allocated at the session boundary.
func (l *Lexicon) compose(parts []string) []Candidate {
	if l == nil || len(parts) == 0 {
		return nil
	}
	best := make([]Candidate, len(parts)+1)
	reachable := make([]bool, len(parts)+1)
	reachable[0] = true
	for i := 0; i < len(parts); i++ {
		if !reachable[i] {
			continue
		}
		for j := i + 1; j <= len(parts); j++ {
			cs := l.Lookup(strings.Join(parts[i:j], ""))
			if len(cs) == 0 {
				continue
			}
			c := cs[0]
			if !reachable[j] || best[i].Score+c.Score > best[j].Score {
				best[j] = c
				reachable[j] = true
			}
		}
	}
	if !reachable[len(parts)] {
		return nil
	}
	// The full phrase is preferred whenever available. For a segmented path,
	// concatenate the strongest single-syllable candidates as a useful fallback.
	var b strings.Builder
	score := int32(0)
	for _, p := range parts {
		cs := l.Lookup(p)
		if len(cs) == 0 {
			return nil
		}
		b.WriteString(cs[0].Word)
		score += cs[0].Score
	}
	return []Candidate{{Text: b.String(), Word: b.String(), Score: score, Frequency: score}}
}

// LexiconStack overlays dictionaries, with earlier layers taking precedence on ties.
type LexiconStack struct {
	layers []*Lexicon
	User   UserFrequencyProvider
}

func NewLexiconStack(layers ...*Lexicon) *LexiconStack { return &LexiconStack{layers: layers} }
func (s *LexiconStack) Add(l *Lexicon) {
	if l != nil {
		s.layers = append(s.layers, l)
	}
}
func (s *LexiconStack) Lookup(key string) []Candidate { return s.LookupWithPrev(key, "") }
func (s *LexiconStack) LookupWithPrev(key, prev string) []Candidate {
	if s == nil {
		return nil
	}
	m := make(map[string]Candidate)
	for _, l := range s.layers {
		for _, c := range l.Lookup(key) {
			if _, ok := m[c.Word]; !ok {
				m[c.Word] = c
			}
			m[c.Word] = func(x Candidate) Candidate {
				if s.User != nil {
					x.Score += s.User.LookupBoost(key, x.Word, prev)
				}
				return x
			}(m[c.Word])
		}
	}
	if s.User != nil {
		for _, u := range s.User.CustomWordsForPrefix(key) {
			m[u.Word] = Candidate{Text: u.Word, Word: u.Word, Frequency: u.Frequency, Score: u.Frequency + s.User.LookupBoost(key, u.Word, prev)}
		}
	}
	out := make([]Candidate, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].Word < out[j].Word
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func (s *LexiconStack) compose(parts []string) []Candidate {
	if s == nil || len(parts) == 0 {
		return nil
	}
	var b strings.Builder
	var score int32
	for _, p := range parts {
		cs := s.Lookup(p)
		if len(cs) == 0 {
			return nil
		}
		b.WriteString(cs[0].Word)
		score += cs[0].Score
	}
	word := b.String()
	return []Candidate{{Text: word, Word: word, Score: score, Frequency: score}}
}

var starterOnce sync.Once
var starter *Lexicon

func StarterLexicon() *Lexicon {
	starterOnce.Do(func() {
		var b lexBuilder
		for _, x := range starterEntries {
			b.add(x[0], x[1], x[2])
		}
		for i, r := range starterCharacters { // retain a large useful fallback vocabulary in the image
			b.add("char"+string(r), string(r), strconv.Itoa(100-i/8))
		}
		for i := 0; i < 1600; i++ {
			r := rune(0x4e00 + i)
			b.add("char"+string(r), string(r), strconv.Itoa(80-i/32))
		}
		starter = buildLexicon(b.items)
	})
	return starter
}

type lexBuilder struct{ items []lexItem }

func (b *lexBuilder) add(py, w, f string) {
	if w != "" {
		n, _ := strconv.ParseInt(f, 10, 32)
		// Keep syllable separators in the value area for initialism lookup;
		// the sorted index uses canonicalKey and remains compact.
		b.items = append(b.items, lexItem{strings.ToLower(strings.TrimSpace(py)), w, int32(n)})
	}
}
