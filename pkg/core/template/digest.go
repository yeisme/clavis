package template

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Digest hashes raw bytes; template bodies are never normalized.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ValidateDigest(value string) error {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return diagnostic(InvalidDigest, "", "expected sha256:<64 lowercase hexadecimal digits>")
	}
	for _, ch := range value[7:] {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return diagnostic(InvalidDigest, "", "expected sha256:<64 lowercase hexadecimal digits>")
		}
	}
	return nil
}

func VerifyDigest(expected string, data []byte) error {
	if err := ValidateDigest(expected); err != nil {
		return err
	}
	if Digest(data) != expected {
		return diagnostic(DigestMismatch, "", "content does not match declared digest")
	}
	return nil
}

func CanonicalCatalogDigest(c Catalog) (string, error) {
	c.Digest = ""
	c.Discovery = nil
	c.GeneratedAt = time.Time{}
	// Copy before sorting, so computing a digest cannot mutate the parsed catalog.
	c.Solutions = append([]Solution(nil), c.Solutions...)
	sort.SliceStable(c.Solutions, func(i, j int) bool { return c.Solutions[i].Key() < c.Solutions[j].Key() })
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return Digest(data), nil
}

func CanonicalSolutionDigest(s Solution) (string, error) {
	s.Digest = ""
	s.RepositoryID = ""
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return Digest(data), nil
}

func VerifyCatalogDigest(c Catalog) error {
	if err := ValidateDigest(c.Digest); err != nil {
		return err
	}
	actual, err := CanonicalCatalogDigest(c)
	if err != nil || actual != c.Digest {
		return diagnostic(DigestMismatch, "", "catalog does not match declared digest")
	}
	return nil
}

func VerifySolutionDigest(s Solution) error {
	if err := ValidateDigest(s.Digest); err != nil {
		return err
	}
	actual, err := CanonicalSolutionDigest(s)
	if err != nil || actual != s.Digest {
		return diagnostic(DigestMismatch, "", "solution does not match declared digest")
	}
	return nil
}

func digestDiagnostic(err error, path string) Diagnostic {
	if d, ok := err.(Diagnostic); ok {
		d.Path = path
		return d
	}
	return diagnostic(InvalidDigest, path, "cannot verify digest")
}
