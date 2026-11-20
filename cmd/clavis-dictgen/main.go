// Command clavis-dictgen builds the embedded common IME dictionary from a Rime TSV/YAML file.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/yeisme/clavis/pkg/core/ime"
)

type metadata struct {
	Version               int    `json:"version"`
	Digest                string `json:"digest"`
	Bytes                 int    `json:"bytes"`
	Source                string `json:"source"`
	License               string `json:"license"`
	Selection             string `json:"selection"`
	WordCount             int    `json:"wordCount"`
	Note                  string `json:"note"`
	Blacklist             string `json:"blacklist"`
	WeightFloorEvaluation string `json:"weightFloorEvaluation,omitempty"`
}

var hanOnly = regexp.MustCompile(`^[\x{4e00}-\x{9fff}]+$`)

func readBlocklist(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]bool{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !hanOnly.MatchString(line) {
			return nil, fmt.Errorf("blocklist entry %q is not pure Han", line)
		}
		out[line] = true
	}
	return out, s.Err()
}

func main() {
	in := flag.String("in", "", "rime base.dict.yaml")
	block := flag.String("blocklist", "pkg/core/inputengine/data/blocklist.txt", "exact-word blocklist")
	out := flag.String("out", "pkg/core/inputengine/data/common.clvdict", "compiled output")
	meta := flag.String("meta", "pkg/core/inputengine/data/common.meta.json", "metadata output")
	minWeight := flag.Int("min-weight", 20000, "inclusive minimum weight")
	maxWeight := flag.Int("max-weight", 600000, "exclusive maximum weight")
	flag.Parse()
	if *in == "" {
		fmt.Fprintln(os.Stderr, "-in is required")
		os.Exit(2)
	}
	blocked, err := readBlocklist(*block)
	if err != nil {
		panic(err)
	}
	src, err := os.Open(*in)
	if err != nil {
		panic(err)
	}
	defer src.Close()
	var b strings.Builder
	b.WriteString("---\nname: clavis-common\nversion: 1\nsort: by_weight\n...\n")
	s := bufio.NewScanner(src)
	count := 0
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "\t") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		word := strings.TrimSpace(fields[0])
		if !hanOnly.MatchString(word) || blocked[word] {
			continue
		}
		weight, err := strconv.Atoi(strings.TrimSpace(strings.Fields(fields[2])[0]))
		if err != nil || weight < *minWeight || weight >= *maxWeight {
			continue
		}
		py := strings.TrimSpace(fields[1])
		if py == "" {
			continue
		}
		b.WriteString(word)
		b.WriteByte('\t')
		b.WriteString(py)
		b.WriteByte('\t')
		b.WriteString(strconv.Itoa(weight))
		b.WriteByte('\n')
		count++
	}
	if err := s.Err(); err != nil {
		panic(err)
	}
	lex, err := ime.CompileRimeDict(strings.NewReader(b.String()))
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(*out, lex.Bytes(), 0644); err != nil {
		panic(err)
	}
	sum := sha256.Sum256(lex.Bytes())
	m := metadata{Version: 1, Digest: "sha256:" + hex.EncodeToString(sum[:]), Bytes: len(lex.Bytes()), Source: "rime-ice cn_dicts/base.dict.yaml (https://github.com/iDvel/rime-ice)", License: "Apache-2.0", Selection: fmt.Sprintf("weight in [%d, %d), pure-Han words only", *minWeight, *maxWeight), WordCount: count, Note: "weight >= 600000 excluded: rime-ice contains easter-egg entries (e.g. 19260817) that would sit on page 1", Blacklist: "pkg/core/inputengine/data/blocklist.txt"}
	if *minWeight == 20000 {
		m.WeightFloorEvaluation = "30000 => 33105 words, 2081908 bytes, 39/40 first-candidate regression. Retained 20000 to preserve the 90% gate."
	}
	data, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(*meta, append(data, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Printf("words=%d bytes=%d digest=%s\n", count, len(lex.Bytes()), m.Digest)
}
