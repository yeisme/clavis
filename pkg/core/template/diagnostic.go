// Package template validates offline promptrepo catalogs and stores verified content.
// Digest verification detects corruption, not authenticity or ownership.
package template

// Code is a deliberately small diagnostic vocabulary. No diagnostic contains a body.
type Code string

const (
	InvalidJSON       Code = "invalid_json"
	UnsupportedSchema Code = "unsupported_schema"
	InvalidCatalog    Code = "invalid_catalog"
	InvalidDigest     Code = "invalid_digest"
	DigestMismatch    Code = "digest_mismatch"
	MissingTemplates  Code = "missing_templates"
	UnsafePath        Code = "unsafe_path"
	DanglingReference Code = "dangling_reference"
	IOError           Code = "io_error"
	CASCorrupt        Code = "cas_corrupt"
)

// Diagnostic identifies an unavailable item without exposing template contents.
type Diagnostic struct {
	Code    Code
	Path    string
	Message string
}

func (d Diagnostic) Error() string { return string(d.Code) + ": " + d.Path + ": " + d.Message }

func diagnostic(code Code, path, message string) Diagnostic {
	return Diagnostic{Code: code, Path: path, Message: message}
}
