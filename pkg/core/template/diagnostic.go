// Package template validates offline promptrepo catalogs and stores verified content.
// Digest verification detects corruption, not authenticity or ownership.
package template

// Code is a deliberately small diagnostic vocabulary. No diagnostic contains a body.
type Code string

const (
	InvalidJSON           Code = "invalid_json"
	UnknownSchema         Code = "unknown_schema"
	UnsupportedSchema     Code = "unsupported_schema" // Public T1 parser code.
	InvalidCatalog        Code = "invalid_catalog"
	InvalidDigest         Code = "invalid_digest"
	DigestMismatch        Code = "digest_mismatch"
	MissingTemplates      Code = "missing_templates"
	UnsafePath            Code = "unsafe_path"
	DanglingReference     Code = "dangling_reference"
	IOError               Code = "io_error"
	CASCorrupt            Code = "cas_corrupt"
	Orphaned              Code = "orphaned"
	DuplicateID           Code = "duplicate_id"
	DegradedNoContract    Code = "degraded_no_contract"
	InvalidOverlay        Code = "invalid_overlay"
	AmbiguousIdentity     Code = "ambiguous_identity"
	UpstreamMismatch      Code = "upstream_mismatch"
	InvalidBody           Code = "invalid_body"
	LocaleUnsupported     Code = "locale_unsupported"
	InputInvalid          Code = "input_invalid"
	DuplicateInput        Code = "duplicate_input"
	BuiltinConflict       Code = "builtin_conflict"
	UndeclaredPlaceholder Code = "undeclared_placeholder"
	MissingRequired       Code = "missing_required"
)

// Diagnostic identifies an unavailable item without exposing template contents.
type Diagnostic struct {
	Code    Code   `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (d Diagnostic) Error() string { return string(d.Code) + ": " + d.Path + ": " + d.Message }

func diagnostic(code Code, path, message string) Diagnostic {
	return Diagnostic{Code: code, Path: path, Message: message}
}
