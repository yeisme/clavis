package toml

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Parse parses a TOML document string into a *Table.
func Parse(input string) (*Table, error) {
	s := newScanner(input)
	return s.parseDocument()
}

// ParseMap parses a TOML document string into map[string]any.
func ParseMap(input string) (map[string]any, error) {
	tbl, err := Parse(input)
	if err != nil {
		return nil, err
	}
	return tbl.ToAny(), nil
}

type tableOrigin uint8

const (
	originImplicit tableOrigin = iota
	originExplicit
	originDotted
	originInline
)

type tableNode struct {
	origin  tableOrigin
	entries map[string]astNode
}

func newTableNode(origin tableOrigin) *tableNode {
	return &tableNode{
		origin:  origin,
		entries: make(map[string]astNode),
	}
}

func (t *tableNode) toTable() *Table {
	out := NewTable()
	for k, n := range t.entries {
		out.Entries[k] = n.toValue()
	}
	return out
}

type nodeKind uint8

const (
	astValue nodeKind = iota
	astTable
	astArrayOfTables
)

type astNode struct {
	kind          nodeKind
	val           Value
	table         *tableNode
	arrayOfTables []*tableNode
}

func (n astNode) toValue() Value {
	switch n.kind {
	case astValue:
		return n.val
	case astTable:
		return TableVal(n.table.toTable())
	case astArrayOfTables:
		items := make([]Value, len(n.arrayOfTables))
		for i, tbl := range n.arrayOfTables {
			items[i] = TableVal(tbl.toTable())
		}
		return ArrayVal(items)
	default:
		return Value{}
	}
}

type scanner struct {
	runes  []rune
	index  int
	line   int
	column int
}

func newScanner(source string) *scanner {
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	return &scanner{
		runes:  []rune(normalized),
		index:  0,
		line:   1,
		column: 1,
	}
}

func (s *scanner) isAtEnd() bool {
	return s.index >= len(s.runes)
}

func (s *scanner) peek(offset int) (rune, bool) {
	target := s.index + offset
	if target < 0 || target >= len(s.runes) {
		return 0, false
	}
	return s.runes[target], true
}

func (s *scanner) advance() (rune, bool) {
	if s.index >= len(s.runes) {
		return 0, false
	}
	r := s.runes[s.index]
	s.index++
	if r == '\n' {
		s.line++
		s.column = 1
	} else {
		s.column++
	}
	return r, true
}

func (s *scanner) errAt(line, col int, msg string) *ParseError {
	return &ParseError{
		Line:    line,
		Column:  col,
		Message: msg,
	}
}

func (s *scanner) errHere(msg string) *ParseError {
	return &ParseError{
		Line:    s.line,
		Column:  s.column,
		Message: msg,
	}
}

func (s *scanner) skipHorizontalWhitespace() {
	for {
		r, ok := s.peek(0)
		if !ok || (r != ' ' && r != '\t') {
			return
		}
		s.advance()
	}
}

func (s *scanner) skipCommentIfPresent() error {
	r, ok := s.peek(0)
	if !ok || r != '#' {
		return nil
	}
	s.advance() // '#'
	for {
		ch, ok := s.peek(0)
		if !ok || ch == '\n' {
			return nil
		}
		if (ch <= 0x08) || (ch >= 0x0A && ch <= 0x1F) || ch == 0x7F {
			return s.errHere("Control character is not allowed inside a comment")
		}
		s.advance()
	}
}

func (s *scanner) skipWhitespaceAndCommentsMultiline() error {
	for {
		r, ok := s.peek(0)
		if !ok {
			return nil
		}
		switch r {
		case ' ', '\t', '\n':
			s.advance()
		case '#':
			if err := s.skipCommentIfPresent(); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func (s *scanner) expectEndOfLineOrEOF() error {
	s.skipHorizontalWhitespace()
	if err := s.skipCommentIfPresent(); err != nil {
		return err
	}
	r, ok := s.peek(0)
	if !ok {
		return nil
	}
	if r == '\n' {
		s.advance()
		return nil
	}
	return s.errHere(fmt.Sprintf("Unexpected trailing character %q", r))
}

func (s *scanner) parseDocument() (*Table, error) {
	root := newTableNode(originExplicit)
	currentTable := root

	for {
		if err := s.skipWhitespaceAndCommentsMultiline(); err != nil {
			return nil, err
		}
		if s.isAtEnd() {
			break
		}
		r, _ := s.peek(0)
		if r == '[' {
			next, ok := s.peek(1)
			var err error
			if ok && next == '[' {
				currentTable, err = s.parseArrayOfTablesHeader(root)
			} else {
				currentTable, err = s.parseTableHeader(root)
			}
			if err != nil {
				return nil, err
			}
		} else {
			if err := s.parseKeyValueAssignment(currentTable, false); err != nil {
				return nil, err
			}
			if err := s.expectEndOfLineOrEOF(); err != nil {
				return nil, err
			}
		}
	}

	return root.toTable(), nil
}

func (s *scanner) parseTableHeader(root *tableNode) (*tableNode, error) {
	headerLine := s.line
	headerCol := s.column
	s.advance() // '['
	s.skipHorizontalWhitespace()
	path, err := s.parseKeyPath()
	if err != nil {
		return nil, err
	}
	s.skipHorizontalWhitespace()
	r, ok := s.peek(0)
	if !ok || r != ']' {
		return nil, s.errHere("Expected ']' to close table header")
	}
	s.advance() // ']'
	if err := s.expectEndOfLineOrEOF(); err != nil {
		return nil, err
	}

	node := root
	for i := 0; i < len(path)-1; i++ {
		node, err = s.navigateOrCreateIntermediateTable(node, path[i], headerLine, headerCol)
		if err != nil {
			return nil, err
		}
	}

	finalKey := path[len(path)-1]
	joined := strings.Join(path, ".")
	if existing, exists := node.entries[finalKey]; exists {
		switch existing.kind {
		case astTable:
			switch existing.table.origin {
			case originImplicit:
				existing.table.origin = originExplicit
				return existing.table, nil
			case originExplicit:
				return nil, s.errAt(headerLine, headerCol, fmt.Sprintf("Table '[%s]' cannot be defined more than once", joined))
			case originDotted:
				return nil, s.errAt(headerLine, headerCol, fmt.Sprintf("Cannot reopen table '[%s]' previously defined via dotted keys", joined))
			case originInline:
				return nil, s.errAt(headerLine, headerCol, fmt.Sprintf("Cannot redefine inline table '[%s]' as a standard table", joined))
			}
		case astArrayOfTables:
			return nil, s.errAt(headerLine, headerCol, fmt.Sprintf("Cannot redefine array of tables '%s' as a standard table", joined))
		case astValue:
			return nil, s.errAt(headerLine, headerCol, fmt.Sprintf("Key '%s' is already defined as a value", joined))
		}
	}

	newTbl := newTableNode(originExplicit)
	node.entries[finalKey] = astNode{kind: astTable, table: newTbl}
	return newTbl, nil
}

func (s *scanner) parseArrayOfTablesHeader(root *tableNode) (*tableNode, error) {
	headerLine := s.line
	headerCol := s.column
	s.advance() // '['
	s.advance() // '['
	s.skipHorizontalWhitespace()
	path, err := s.parseKeyPath()
	if err != nil {
		return nil, err
	}
	s.skipHorizontalWhitespace()
	r0, ok0 := s.peek(0)
	r1, ok1 := s.peek(1)
	if !ok0 || !ok1 || r0 != ']' || r1 != ']' {
		return nil, s.errHere("Expected ']]' to close array of tables header")
	}
	s.advance() // ']'
	s.advance() // ']'
	if err := s.expectEndOfLineOrEOF(); err != nil {
		return nil, err
	}

	node := root
	for i := 0; i < len(path)-1; i++ {
		node, err = s.navigateOrCreateIntermediateTable(node, path[i], headerLine, headerCol)
		if err != nil {
			return nil, err
		}
	}

	finalKey := path[len(path)-1]
	joined := strings.Join(path, ".")
	if existing, exists := node.entries[finalKey]; exists {
		switch existing.kind {
		case astArrayOfTables:
			newTbl := newTableNode(originExplicit)
			existing.arrayOfTables = append(existing.arrayOfTables, newTbl)
			node.entries[finalKey] = existing
			return newTbl, nil
		case astTable:
			return nil, s.errAt(headerLine, headerCol, fmt.Sprintf("Key '%s' is already defined as a table", joined))
		case astValue:
			return nil, s.errAt(headerLine, headerCol, fmt.Sprintf("Key '%s' is already defined as a non-table value", joined))
		}
	}

	newTbl := newTableNode(originExplicit)
	node.entries[finalKey] = astNode{
		kind:          astArrayOfTables,
		arrayOfTables: []*tableNode{newTbl},
	}
	return newTbl, nil
}

func (s *scanner) navigateOrCreateIntermediateTable(parent *tableNode, key string, line, col int) (*tableNode, error) {
	if existing, exists := parent.entries[key]; exists {
		switch existing.kind {
		case astTable:
			if existing.table.origin == originInline {
				return nil, s.errAt(line, col, fmt.Sprintf("Cannot extend inline table '%s'", key))
			}
			if existing.table.origin == originDotted {
				return nil, s.errAt(line, col, fmt.Sprintf("Cannot define sub-table under dotted key '%s'", key))
			}
			return existing.table, nil
		case astArrayOfTables:
			if len(existing.arrayOfTables) == 0 {
				return nil, s.errAt(line, col, fmt.Sprintf("Array of tables '%s' is unexpectedly empty", key))
			}
			return existing.arrayOfTables[len(existing.arrayOfTables)-1], nil
		case astValue:
			return nil, s.errAt(line, col, fmt.Sprintf("Key '%s' is already defined as a scalar value, not a table", key))
		}
	}
	implicitTbl := newTableNode(originImplicit)
	parent.entries[key] = astNode{kind: astTable, table: implicitTbl}
	return implicitTbl, nil
}

func (s *scanner) parseKeyValueAssignment(table *tableNode, isInline bool) error {
	keyLine := s.line
	keyCol := s.column
	path, err := s.parseKeyPath()
	if err != nil {
		return err
	}

	s.skipHorizontalWhitespace()
	r, ok := s.peek(0)
	if !ok || r != '=' {
		return s.errHere(fmt.Sprintf("Expected '=' after key '%s'", strings.Join(path, ".")))
	}
	s.advance() // '='
	s.skipHorizontalWhitespace()

	valNode, err := s.parseValueNode()
	if err != nil {
		return err
	}

	target := table
	for i := 0; i < len(path)-1; i++ {
		key := path[i]
		if existing, exists := target.entries[key]; exists {
			switch existing.kind {
			case astTable:
				if existing.table.origin == originInline {
					return s.errAt(keyLine, keyCol, fmt.Sprintf("Cannot add key to closed inline table '%s'", key))
				}
				if existing.table.origin == originExplicit {
					return s.errAt(keyLine, keyCol, fmt.Sprintf("Cannot add dotted key to explicitly defined table '%s'", key))
				}
				if existing.table.origin == originImplicit && !isInline {
					existing.table.origin = originDotted
				}
				target = existing.table
			case astArrayOfTables, astValue:
				return s.errAt(keyLine, keyCol, fmt.Sprintf("Cannot traverse through non-table key '%s'", key))
			}
		} else {
			dottedTbl := newTableNode(originDotted)
			target.entries[key] = astNode{kind: astTable, table: dottedTbl}
			target = dottedTbl
		}
	}

	finalKey := path[len(path)-1]
	if _, exists := target.entries[finalKey]; exists {
		return s.errAt(keyLine, keyCol, fmt.Sprintf("Duplicate key '%s'", strings.Join(path, ".")))
	}
	target.entries[finalKey] = valNode
	return nil
}

func (s *scanner) parseKeyPath() ([]string, error) {
	var keys []string
	k, err := s.parseSingleKey()
	if err != nil {
		return nil, err
	}
	keys = append(keys, k)

	for {
		s.skipHorizontalWhitespace()
		r, ok := s.peek(0)
		if ok && r == '.' {
			s.advance()
			s.skipHorizontalWhitespace()
			k, err := s.parseSingleKey()
			if err != nil {
				return nil, err
			}
			keys = append(keys, k)
		} else {
			break
		}
	}
	return keys, nil
}

func (s *scanner) parseSingleKey() (string, error) {
	r, ok := s.peek(0)
	if !ok {
		return "", s.errHere("Expected key, found end of input")
	}
	if r == '"' {
		r1, _ := s.peek(1)
		r2, _ := s.peek(2)
		if r1 == '"' && r2 == '"' {
			return "", s.errHere("Multiline basic strings cannot be used as keys")
		}
		return s.parseBasicString()
	}
	if r == '\'' {
		r1, _ := s.peek(1)
		r2, _ := s.peek(2)
		if r1 == '\'' && r2 == '\'' {
			return "", s.errHere("Multiline literal strings cannot be used as keys")
		}
		return s.parseLiteralString()
	}
	if isBareKeyRune(r) {
		var sb strings.Builder
		for {
			ch, ok := s.peek(0)
			if !ok || !isBareKeyRune(ch) {
				break
			}
			sb.WriteRune(ch)
			s.advance()
		}
		return sb.String(), nil
	}
	return "", s.errHere(fmt.Sprintf("Invalid character %q in key", r))
}

func isBareKeyRune(r rune) bool {
	return (r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '-'
}

func (s *scanner) parseValueNode() (astNode, error) {
	r, ok := s.peek(0)
	if !ok {
		return astNode{}, s.errHere("Expected value, found end of input")
	}

	switch {
	case r == '"':
		r1, _ := s.peek(1)
		r2, _ := s.peek(2)
		if r1 == '"' && r2 == '"' {
			str, err := s.parseMultilineBasicString()
			if err != nil {
				return astNode{}, err
			}
			return astNode{kind: astValue, val: StringVal(str)}, nil
		}
		str, err := s.parseBasicString()
		if err != nil {
			return astNode{}, err
		}
		return astNode{kind: astValue, val: StringVal(str)}, nil

	case r == '\'':
		r1, _ := s.peek(1)
		r2, _ := s.peek(2)
		if r1 == '\'' && r2 == '\'' {
			str, err := s.parseMultilineLiteralString()
			if err != nil {
				return astNode{}, err
			}
			return astNode{kind: astValue, val: StringVal(str)}, nil
		}
		str, err := s.parseLiteralString()
		if err != nil {
			return astNode{}, err
		}
		return astNode{kind: astValue, val: StringVal(str)}, nil

	case r == '[':
		val, err := s.parseArray()
		if err != nil {
			return astNode{}, err
		}
		return astNode{kind: astValue, val: val}, nil

	case r == '{':
		tbl, err := s.parseInlineTable()
		if err != nil {
			return astNode{}, err
		}
		return astNode{kind: astTable, table: tbl}, nil

	case r == 't' || r == 'f':
		val, err := s.parseBoolean()
		if err != nil {
			return astNode{}, err
		}
		return astNode{kind: astValue, val: val}, nil

	case r == '+' || r == '-' || (r >= '0' && r <= '9') || r == 'i' || r == 'n':
		val, err := s.parseNumberOrSpecialFloat()
		if err != nil {
			return astNode{}, err
		}
		return astNode{kind: astValue, val: val}, nil

	default:
		return astNode{}, s.errHere(fmt.Sprintf("Unexpected token %q while parsing value", r))
	}
}

func (s *scanner) parseBasicString() (string, error) {
	startLine := s.line
	startCol := s.column
	s.advance() // '"'
	var sb strings.Builder

	for {
		ch, ok := s.peek(0)
		if !ok {
			return "", s.errAt(startLine, startCol, "Unterminated basic string")
		}
		if ch == '"' {
			s.advance()
			return sb.String(), nil
		}
		if ch == '\\' {
			s.advance()
			esc, err := s.parseEscapeSequence()
			if err != nil {
				return "", err
			}
			sb.WriteRune(esc)
			continue
		}
		if ch == '\n' {
			return "", s.errAt(startLine, startCol, "Newline is not allowed in single-line basic string")
		}
		if err := s.validateNonControlRune(ch); err != nil {
			return "", err
		}
		sb.WriteRune(ch)
		s.advance()
	}
}

func (s *scanner) parseMultilineBasicString() (string, error) {
	startLine := s.line
	startCol := s.column
	s.advance()
	s.advance()
	s.advance() // '"""'

	if r, ok := s.peek(0); ok && r == '\n' {
		s.advance()
	}

	var sb strings.Builder
	for {
		ch, ok := s.peek(0)
		if !ok {
			return "", s.errAt(startLine, startCol, "Unterminated multiline basic string")
		}
		r1, _ := s.peek(1)
		r2, _ := s.peek(2)
		if ch == '"' && r1 == '"' && r2 == '"' {
			quoteCount := 0
			for quoteCount < 5 {
				q, qok := s.peek(0)
				if !qok || q != '"' {
					break
				}
				s.advance()
				quoteCount++
			}
			for i := 0; i < quoteCount-3; i++ {
				sb.WriteByte('"')
			}
			return sb.String(), nil
		}
		if ch == '\\' {
			s.advance()
			if s.isLineEndingBackslash() {
				s.skipLineEndingBackslashWhitespace()
			} else {
				esc, err := s.parseEscapeSequence()
				if err != nil {
					return "", err
				}
				sb.WriteRune(esc)
			}
			continue
		}
		if ch != '\n' {
			if err := s.validateNonControlRune(ch); err != nil {
				return "", err
			}
		}
		sb.WriteRune(ch)
		s.advance()
	}
}

func (s *scanner) isLineEndingBackslash() bool {
	offset := 0
	for {
		r, ok := s.peek(offset)
		if !ok {
			return false
		}
		if r == ' ' || r == '\t' {
			offset++
		} else if r == '\n' {
			return true
		} else {
			return false
		}
	}
}

func (s *scanner) skipLineEndingBackslashWhitespace() {
	for {
		r, ok := s.peek(0)
		if !ok || (r != ' ' && r != '\t' && r != '\n') {
			return
		}
		s.advance()
	}
}

func (s *scanner) parseEscapeSequence() (rune, error) {
	escLine := s.line
	escCol := s.column
	esc, ok := s.advance()
	if !ok {
		return 0, s.errAt(escLine, escCol, "Unterminated escape sequence")
	}
	switch esc {
	case 'b':
		return '\b', nil
	case 't':
		return '\t', nil
	case 'n':
		return '\n', nil
	case 'f':
		return '\f', nil
	case 'r':
		return '\r', nil
	case '"':
		return '"', nil
	case '\\':
		return '\\', nil
	case 'u':
		return s.parseUnicodeScalar(4)
	case 'U':
		return s.parseUnicodeScalar(8)
	default:
		return 0, s.errAt(escLine, escCol, fmt.Sprintf("Invalid escape sequence '\\%c'", esc))
	}
}

func (s *scanner) parseUnicodeScalar(digits int) (rune, error) {
	var sb strings.Builder
	for i := 0; i < digits; i++ {
		ch, ok := s.advance()
		if !ok || !isHexDigit(ch) {
			return 0, s.errHere(fmt.Sprintf("Invalid unicode escape sequence; expected %d hex digits", digits))
		}
		sb.WriteRune(ch)
	}
	v, err := strconv.ParseUint(sb.String(), 16, 32)
	if err != nil {
		return 0, s.errHere(fmt.Sprintf("Invalid unicode scalar value '\\u%s'", sb.String()))
	}
	r := rune(v)
	if !utf8.ValidRune(r) {
		return 0, s.errHere(fmt.Sprintf("Invalid unicode scalar value '\\u%s'", sb.String()))
	}
	return r, nil
}

func (s *scanner) parseLiteralString() (string, error) {
	startLine := s.line
	startCol := s.column
	s.advance() // '\''
	var sb strings.Builder

	for {
		ch, ok := s.peek(0)
		if !ok {
			return "", s.errAt(startLine, startCol, "Unterminated literal string")
		}
		if ch == '\'' {
			s.advance()
			return sb.String(), nil
		}
		if ch == '\n' {
			return "", s.errAt(startLine, startCol, "Newline is not allowed in single-line literal string")
		}
		if err := s.validateNonControlRune(ch); err != nil {
			return "", err
		}
		sb.WriteRune(ch)
		s.advance()
	}
}

func (s *scanner) parseMultilineLiteralString() (string, error) {
	startLine := s.line
	startCol := s.column
	s.advance()
	s.advance()
	s.advance() // "'''"

	if r, ok := s.peek(0); ok && r == '\n' {
		s.advance()
	}

	var sb strings.Builder
	for {
		ch, ok := s.peek(0)
		if !ok {
			return "", s.errAt(startLine, startCol, "Unterminated multiline literal string")
		}
		r1, _ := s.peek(1)
		r2, _ := s.peek(2)
		if ch == '\'' && r1 == '\'' && r2 == '\'' {
			quoteCount := 0
			for quoteCount < 5 {
				q, qok := s.peek(0)
				if !qok || q != '\'' {
					break
				}
				s.advance()
				quoteCount++
			}
			for i := 0; i < quoteCount-3; i++ {
				sb.WriteByte('\'')
			}
			return sb.String(), nil
		}
		if ch != '\n' {
			if err := s.validateNonControlRune(ch); err != nil {
				return "", err
			}
		}
		sb.WriteRune(ch)
		s.advance()
	}
}

func (s *scanner) validateNonControlRune(r rune) error {
	if (r <= 0x08) || (r >= 0x0A && r <= 0x1F) || r == 0x7F {
		return s.errHere(fmt.Sprintf("Unescaped control character (U+%04X) is not allowed in string", r))
	}
	return nil
}

func (s *scanner) parseArray() (Value, error) {
	startLine := s.line
	startCol := s.column
	s.advance() // '['
	var items []Value

	for {
		if err := s.skipWhitespaceAndCommentsMultiline(); err != nil {
			return Value{}, err
		}
		if s.isAtEnd() {
			return Value{}, s.errAt(startLine, startCol, "Unterminated array")
		}
		if r, _ := s.peek(0); r == ']' {
			s.advance()
			return ArrayVal(items), nil
		}

		elemNode, err := s.parseValueNode()
		if err != nil {
			return Value{}, err
		}
		items = append(items, elemNode.toValue())

		if err := s.skipWhitespaceAndCommentsMultiline(); err != nil {
			return Value{}, err
		}
		r, ok := s.peek(0)
		if ok && r == ',' {
			s.advance()
		} else if ok && r == ']' {
			s.advance()
			return ArrayVal(items), nil
		} else {
			return Value{}, s.errHere("Expected ',' or ']' in array")
		}
	}
}

func (s *scanner) parseInlineTable() (*tableNode, error) {
	startLine := s.line
	startCol := s.column
	s.advance() // '{'
	tbl := newTableNode(originInline)

	s.skipHorizontalWhitespace()
	if r, ok := s.peek(0); ok && r == '}' {
		s.advance()
		return tbl, nil
	}

	for {
		s.skipHorizontalWhitespace()
		r, ok := s.peek(0)
		if !ok || r == '\n' {
			return nil, s.errAt(startLine, startCol, "Unterminated inline table")
		}

		if err := s.parseKeyValueAssignment(tbl, true); err != nil {
			return nil, err
		}
		s.skipHorizontalWhitespace()

		next, ok := s.peek(0)
		if ok && next == ',' {
			s.advance()
			s.skipHorizontalWhitespace()
			if afterComma, aok := s.peek(0); aok && afterComma == '}' {
				return nil, s.errHere("Trailing comma is not allowed in inline table")
			}
		} else if ok && next == '}' {
			s.advance()
			return tbl, nil
		} else {
			return nil, s.errHere("Expected ',' or '}' in inline table")
		}
	}
}

func (s *scanner) parseBoolean() (Value, error) {
	startLine := s.line
	startCol := s.column
	token := s.consumeToken()
	switch token {
	case "true":
		return BoolVal(true), nil
	case "false":
		return BoolVal(false), nil
	default:
		return Value{}, s.errAt(startLine, startCol, fmt.Sprintf("Invalid boolean literal '%s'", token))
	}
}

func (s *scanner) parseNumberOrSpecialFloat() (Value, error) {
	startLine := s.line
	startCol := s.column
	token := s.consumeToken()

	switch token {
	case "inf", "+inf":
		return FloatVal(math.Inf(1)), nil
	case "-inf":
		return FloatVal(math.Inf(-1)), nil
	case "nan", "+nan", "-nan":
		return FloatVal(math.NaN()), nil
	}

	if strings.HasPrefix(token, "0x") || strings.HasPrefix(token, "0o") || strings.HasPrefix(token, "0b") {
		return s.parseRadixInteger(token, startLine, startCol)
	}
	if strings.HasPrefix(token, "+0x") || strings.HasPrefix(token, "-0x") ||
		strings.HasPrefix(token, "+0o") || strings.HasPrefix(token, "-0o") ||
		strings.HasPrefix(token, "+0b") || strings.HasPrefix(token, "-0b") {
		return Value{}, s.errAt(startLine, startCol, fmt.Sprintf("Prefixed integer '%s' cannot have a sign", token))
	}

	if strings.ContainsAny(token, ".eE") {
		return s.parseDecimalFloat(token, startLine, startCol)
	}
	return s.parseDecimalInteger(token, startLine, startCol)
}

func (s *scanner) consumeToken() string {
	var sb strings.Builder
	for {
		r, ok := s.peek(0)
		if !ok || isValueDelimiter(r) {
			return sb.String()
		}
		sb.WriteRune(r)
		s.advance()
	}
}

func isValueDelimiter(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '#' || r == ',' || r == ']' || r == '}'
}

func (s *scanner) parseRadixInteger(token string, line, col int) (Value, error) {
	prefix := token[:2]
	body := token[2:]
	if body == "" {
		return Value{}, s.errAt(line, col, fmt.Sprintf("Invalid integer '%s'", token))
	}

	var base int
	var validDigit func(rune) bool
	switch prefix {
	case "0x":
		base = 16
		validDigit = isHexDigit
	case "0o":
		base = 8
		validDigit = func(r rune) bool { return r >= '0' && r <= '7' }
	case "0b":
		base = 2
		validDigit = func(r rune) bool { return r == '0' || r == '1' }
	default:
		return Value{}, s.errAt(line, col, fmt.Sprintf("Invalid integer prefix '%s'", prefix))
	}

	cleaned, err := s.validateAndStripUnderscores(body, validDigit, token, line, col)
	if err != nil {
		return Value{}, err
	}
	val, err := strconv.ParseInt(cleaned, base, 64)
	if err != nil {
		return Value{}, s.errAt(line, col, fmt.Sprintf("Integer '%s' is out of 64-bit signed range", token))
	}
	return IntVal(val), nil
}

func (s *scanner) parseDecimalInteger(token string, line, col int) (Value, error) {
	rest := token
	sign := ""
	if strings.HasPrefix(rest, "+") || strings.HasPrefix(rest, "-") {
		sign = rest[:1]
		rest = rest[1:]
	}
	if rest == "" {
		return Value{}, s.errAt(line, col, fmt.Sprintf("Invalid integer '%s'", token))
	}

	cleaned, err := s.validateAndStripUnderscores(rest, isASCIIDigit, token, line, col)
	if err != nil {
		return Value{}, err
	}
	if len(cleaned) > 1 && cleaned[0] == '0' {
		return Value{}, s.errAt(line, col, fmt.Sprintf("Leading zeros are not allowed in integer '%s'", token))
	}

	val, err := strconv.ParseInt(sign+cleaned, 10, 64)
	if err != nil {
		return Value{}, s.errAt(line, col, fmt.Sprintf("Integer '%s' is out of 64-bit signed range", token))
	}
	return IntVal(val), nil
}

func (s *scanner) parseDecimalFloat(token string, line, col int) (Value, error) {
	rest := token
	sign := ""
	if strings.HasPrefix(rest, "+") || strings.HasPrefix(rest, "-") {
		sign = rest[:1]
		rest = rest[1:]
	}
	if rest == "" {
		return Value{}, s.errAt(line, col, fmt.Sprintf("Invalid float '%s'", token))
	}

	var mantissaPart, expPart string
	hasExp := false
	if idx := strings.IndexAny(rest, "eE"); idx >= 0 {
		mantissaPart = rest[:idx]
		expPart = rest[idx+1:]
		hasExp = true
	} else {
		mantissaPart = rest
	}

	var cleanedMantissa string
	if dotIdx := strings.IndexByte(mantissaPart, '.'); dotIdx >= 0 {
		intPart := mantissaPart[:dotIdx]
		fracPart := mantissaPart[dotIdx+1:]
		if intPart == "" || fracPart == "" || strings.ContainsRune(fracPart, '.') {
			return Value{}, s.errAt(line, col, fmt.Sprintf("Invalid float '%s'", token))
		}
		cleanedInt, err := s.validateAndStripUnderscores(intPart, isASCIIDigit, token, line, col)
		if err != nil {
			return Value{}, err
		}
		if len(cleanedInt) > 1 && cleanedInt[0] == '0' {
			return Value{}, s.errAt(line, col, fmt.Sprintf("Leading zeros are not allowed in float '%s'", token))
		}
		cleanedFrac, err := s.validateAndStripUnderscores(fracPart, isASCIIDigit, token, line, col)
		if err != nil {
			return Value{}, err
		}
		cleanedMantissa = cleanedInt + "." + cleanedFrac
	} else {
		if !hasExp {
			return Value{}, s.errAt(line, col, fmt.Sprintf("Invalid float '%s'", token))
		}
		cleanedInt, err := s.validateAndStripUnderscores(mantissaPart, isASCIIDigit, token, line, col)
		if err != nil {
			return Value{}, err
		}
		if len(cleanedInt) > 1 && cleanedInt[0] == '0' {
			return Value{}, s.errAt(line, col, fmt.Sprintf("Leading zeros are not allowed in float '%s'", token))
		}
		cleanedMantissa = cleanedInt
	}

	assembled := sign + cleanedMantissa
	if hasExp {
		expSign := ""
		if strings.HasPrefix(expPart, "+") || strings.HasPrefix(expPart, "-") {
			expSign = expPart[:1]
			expPart = expPart[1:]
		}
		if expPart == "" {
			return Value{}, s.errAt(line, col, fmt.Sprintf("Invalid float exponent in '%s'", token))
		}
		cleanedExp, err := s.validateAndStripUnderscores(expPart, isASCIIDigit, token, line, col)
		if err != nil {
			return Value{}, err
		}
		assembled += "e" + expSign + cleanedExp
	}

	val, err := strconv.ParseFloat(assembled, 64)
	if err != nil {
		return Value{}, s.errAt(line, col, fmt.Sprintf("Invalid float '%s'", token))
	}
	return FloatVal(val), nil
}

func (s *scanner) validateAndStripUnderscores(raw string, validDigit func(rune) bool, original string, line, col int) (string, error) {
	if raw == "" {
		return "", s.errAt(line, col, fmt.Sprintf("Invalid number '%s'", original))
	}
	var sb strings.Builder
	sb.Grow(len(raw))
	prevUnderscore := true

	for _, r := range raw {
		if r == '_' {
			if prevUnderscore {
				return "", s.errAt(line, col, fmt.Sprintf("Invalid underscore placement in number '%s'", original))
			}
			prevUnderscore = true
		} else if validDigit(r) {
			sb.WriteRune(r)
			prevUnderscore = false
		} else {
			return "", s.errAt(line, col, fmt.Sprintf("Invalid character %q in number '%s'", r, original))
		}
	}

	if prevUnderscore || sb.Len() == 0 {
		return "", s.errAt(line, col, fmt.Sprintf("Invalid trailing underscore in number '%s'", original))
	}
	return sb.String(), nil
}

func isASCIIDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}
