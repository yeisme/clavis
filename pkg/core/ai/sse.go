package ai

import (
	"encoding/json"
	"strings"
)

// SSEEventKind identifies the type of parsed SSE event.
type SSEEventKind uint8

const (
	SSEDelta SSEEventKind = iota + 1
	SSEDone
	SSEError
)

// SSEEvent represents a parsed SSE event from an OpenAI-compatible stream.
type SSEEvent struct {
	Kind SSEEventKind
	Text string
}

// SSEStreamParser incrementally parses byte chunks into SSEEvents.
// It buffers partial lines and multi-byte UTF-8 sequences safely across Feed calls.
type SSEStreamParser struct {
	buf           []byte
	eventName     string
	eventID       string
	dataLines     []string
	didEmitDone   bool
}

// Feed appends incoming bytes and returns any complete SSEEvents parsed so far.
func (p *SSEStreamParser) Feed(chunk []byte) []SSEEvent {
	if len(chunk) == 0 {
		return nil
	}
	p.buf = append(p.buf, chunk...)
	return p.drainCompleteLines(false)
}

// Finish flushes any remaining buffered bytes at EOF and dispatches any pending event.
func (p *SSEStreamParser) Finish() []SSEEvent {
	events := p.drainCompleteLines(true)
	if len(p.buf) > 0 {
		line := string(p.buf)
		p.buf = nil
		if ev, ok := p.processLine(line); ok {
			events = append(events, ev)
		}
	}
	if ev, ok := p.dispatchCurrentEvent(); ok {
		events = append(events, ev)
	}
	return events
}

func (p *SSEStreamParser) drainCompleteLines(isEOF bool) []SSEEvent {
	var events []SSEEvent
	lineStart := 0
	i := 0

	for i < len(p.buf) {
		b := p.buf[i]
		if b == '\n' {
			line := string(p.buf[lineStart:i])
			if ev, ok := p.processLine(line); ok {
				events = append(events, ev)
			}
			i++
			lineStart = i
		} else if b == '\r' {
			if i+1 < len(p.buf) {
				line := string(p.buf[lineStart:i])
				if ev, ok := p.processLine(line); ok {
					events = append(events, ev)
				}
				if p.buf[i+1] == '\n' {
					i += 2
				} else {
					i++
				}
				lineStart = i
			} else if isEOF {
				line := string(p.buf[lineStart:i])
				if ev, ok := p.processLine(line); ok {
					events = append(events, ev)
				}
				i++
				lineStart = i
			} else {
				// Wait for next chunk to distinguish '\r\n' from lone '\r'.
				break
			}
		} else {
			i++
		}
	}

	if lineStart > 0 {
		remaining := len(p.buf) - lineStart
		copy(p.buf, p.buf[lineStart:])
		p.buf = p.buf[:remaining]
	}
	return events
}

func (p *SSEStreamParser) processLine(line string) (SSEEvent, bool) {
	if line == "" {
		return p.dispatchCurrentEvent()
	}
	if strings.HasPrefix(line, ":") {
		return SSEEvent{}, false
	}

	var field, value string
	if colonIdx := strings.IndexByte(line, ':'); colonIdx >= 0 {
		field = line[:colonIdx]
		rest := line[colonIdx+1:]
		if strings.HasPrefix(rest, " ") {
			value = rest[1:]
		} else {
			value = rest
		}
	} else {
		field = line
		value = ""
	}

	switch field {
	case "data":
		p.dataLines = append(p.dataLines, value)
	case "event":
		p.eventName = value
	case "id":
		p.eventID = value
	}
	return SSEEvent{}, false
}

func (p *SSEStreamParser) dispatchCurrentEvent() (SSEEvent, bool) {
	eventName := p.eventName
	lines := p.dataLines

	p.eventName = ""
	p.eventID = ""
	p.dataLines = nil

	if len(lines) == 0 {
		return SSEEvent{}, false
	}

	payload := strings.Join(lines, "\n")
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return SSEEvent{}, false
	}
	if trimmed == "[DONE]" {
		p.didEmitDone = true
		return SSEEvent{Kind: SSEDone}, true
	}

	var rawMap map[string]any
	if err := json.Unmarshal([]byte(trimmed), &rawMap); err != nil {
		if eventName == "error" {
			return SSEEvent{Kind: SSEError, Text: trimmed}, true
		}
		return SSEEvent{Kind: SSEError, Text: "Malformed SSE JSON payload: " + err.Error()}, true
	}

	if errMsg := extractErrorMessage(rawMap); errMsg != "" {
		return SSEEvent{Kind: SSEError, Text: errMsg}, true
	}
	if eventName == "error" {
		return SSEEvent{Kind: SSEError, Text: trimmed}, true
	}

	if choices, ok := rawMap["choices"].([]any); ok && len(choices) > 0 {
		if first, ok := choices[0].(map[string]any); ok {
			if delta, ok := first["delta"].(map[string]any); ok {
				if content, ok := delta["content"].(string); ok && content != "" {
					return SSEEvent{Kind: SSEDelta, Text: content}, true
				}
			}
			if msg, ok := first["message"].(map[string]any); ok {
				if content, ok := msg["content"].(string); ok && content != "" {
					return SSEEvent{Kind: SSEDelta, Text: content}, true
				}
			}
		}
	}

	return SSEEvent{}, false
}

func extractErrorMessage(dict map[string]any) string {
	val, exists := dict["error"]
	if !exists || val == nil {
		return ""
	}
	switch v := val.(type) {
	case map[string]any:
		if msg, ok := v["message"].(string); ok && msg != "" {
			return msg
		}
		if typ, ok := v["type"].(string); ok && typ != "" {
			return typ
		}
		return "Unknown AI API error"
	case string:
		if v != "" {
			return v
		}
	}
	return "Unknown AI API error"
}
