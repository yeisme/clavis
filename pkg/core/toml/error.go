package toml

import "fmt"

// ParseError describes a TOML syntax or structural error with 1-based location.
type ParseError struct {
	Line    int
	Column  int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Message)
}
