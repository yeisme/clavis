package ai

import (
	"encoding/json"
	"strings"

	"github.com/yeisme/clavis/pkg/core/config"
)

// SSEEventKind identifies the type of parsed SSE event.
type SSEEventKind uint8

const (
	SSEDelta SSEEventKind = iota + 1
	SSEDone
	SSEError
	SSEUsage
)

// SSEEvent represents a parsed SSE event from an AI streaming response.
type SSEEvent struct {
	Kind  SSEEventKind
	Text  string
	Usage ProviderUsage
}

// SSEStreamParser incrementally parses byte chunks into SSEEvents.
// It buffers partial lines and multi-byte UTF-8 sequences safely across Feed calls,
// and supports OpenAI-compatible, Anthropic, and Gemini SSE wire formats.
type SSEStreamParser struct {
	Protocol    config.AIProtocol
	buf         []byte
	eventName   string
	eventID     string
	dataLines   []string
	didEmitDone bool
	usage       ProviderUsage
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
		events = append(events, p.processLine(line)...)
	}
	events = append(events, p.dispatchCurrentEvent()...)
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
			events = append(events, p.processLine(line)...)
			i++
			lineStart = i
		} else if b == '\r' {
			if i+1 < len(p.buf) {
				line := string(p.buf[lineStart:i])
				events = append(events, p.processLine(line)...)
				if p.buf[i+1] == '\n' {
					i += 2
				} else {
					i++
				}
				lineStart = i
			} else if isEOF {
				line := string(p.buf[lineStart:i])
				events = append(events, p.processLine(line)...)
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

func (p *SSEStreamParser) processLine(line string) []SSEEvent {
	if line == "" {
		return p.dispatchCurrentEvent()
	}
	if strings.HasPrefix(line, ":") {
		return nil
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
		p.eventName = strings.TrimSpace(value)
	case "id":
		p.eventID = value
	}
	return nil
}

func (p *SSEStreamParser) dispatchCurrentEvent() []SSEEvent {
	eventName := p.eventName
	lines := p.dataLines

	p.eventName = ""
	p.eventID = ""
	p.dataLines = nil

	proto := p.Protocol
	if proto == "" {
		proto = config.ProtocolOpenAICompatible
	}

	if proto == config.ProtocolAnthropic && eventName == "message_stop" && len(lines) == 0 {
		p.didEmitDone = true
		return []SSEEvent{{Kind: SSEDone}}
	}

	if len(lines) == 0 {
		return nil
	}

	payload := strings.Join(lines, "\n")
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil
	}
	if trimmed == "[DONE]" {
		p.didEmitDone = true
		return []SSEEvent{{Kind: SSEDone}}
	}

	var rawMap map[string]any
	if err := json.Unmarshal([]byte(trimmed), &rawMap); err != nil {
		if eventName == "error" {
			return []SSEEvent{{Kind: SSEError, Text: trimmed}}
		}
		return []SSEEvent{{Kind: SSEError, Text: "Malformed SSE JSON payload: " + err.Error()}}
	}

	usage := mergeProviderUsage(p.usage, []byte(trimmed))
	if usage.Reported {
		p.usage = usage
	}
	if errMsg := extractErrorMessage(rawMap); errMsg != "" {
		return []SSEEvent{{Kind: SSEError, Text: errMsg}}
	}
	if eventName == "error" || rawMap["type"] == "error" {
		return []SSEEvent{{Kind: SSEError, Text: trimmed}}
	}
	switch proto {
	case config.ProtocolAnthropic:
		msgType, _ := rawMap["type"].(string)
		if eventName == "message_stop" || msgType == "message_stop" {
			p.didEmitDone = true
			return []SSEEvent{{Kind: SSEDone}}
		}
		if eventName == "content_block_delta" || msgType == "content_block_delta" {
			if delta, ok := rawMap["delta"].(map[string]any); ok {
				deltaType, _ := delta["type"].(string)
				if deltaType == "text_delta" || deltaType == "" {
					if text, ok := delta["text"].(string); ok && text != "" {
						out := []SSEEvent{{Kind: SSEDelta, Text: text}}
						if usage.Reported {
							out = append(out, SSEEvent{Kind: SSEUsage, Usage: usage})
						}
						return out
					}
				}
			}
		}
		if usage.Reported {
			return []SSEEvent{{Kind: SSEUsage, Usage: usage}}
		}
		return nil

	case config.ProtocolGemini:
		var out []SSEEvent
		if candidates, ok := rawMap["candidates"].([]any); ok && len(candidates) > 0 {
			if first, ok := candidates[0].(map[string]any); ok {
				if contentObj, ok := first["content"].(map[string]any); ok {
					if parts, ok := contentObj["parts"].([]any); ok {
						var sb strings.Builder
						for _, part := range parts {
							if pm, ok := part.(map[string]any); ok {
								if t, ok := pm["text"].(string); ok {
									sb.WriteString(t)
								}
							}
						}
						if sb.Len() > 0 {
							out = append(out, SSEEvent{Kind: SSEDelta, Text: sb.String()})
						}
					}
				}
				if fr, ok := first["finishReason"].(string); ok && fr != "" {
					p.didEmitDone = true
					out = append(out, SSEEvent{Kind: SSEDone})
				}
			}
		}
		if usage.Reported {
			out = append(out, SSEEvent{Kind: SSEUsage, Usage: usage})
		}
		return out

	default:
		var out []SSEEvent
		if choices, ok := rawMap["choices"].([]any); ok && len(choices) > 0 {
			if first, ok := choices[0].(map[string]any); ok {
				if delta, ok := first["delta"].(map[string]any); ok {
					if content, ok := delta["content"].(string); ok && content != "" {
						out = append(out, SSEEvent{Kind: SSEDelta, Text: content})
					}
				}
				if msg, ok := first["message"].(map[string]any); ok {
					if content, ok := msg["content"].(string); ok && content != "" {
						out = append(out, SSEEvent{Kind: SSEDelta, Text: content})
					}
				}
			}
		}
		if usage.Reported {
			out = append(out, SSEEvent{Kind: SSEUsage, Usage: usage})
		}
		return out
	}
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
