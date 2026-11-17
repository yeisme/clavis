// Package pluginprotocol implements the JSON-RPC 2.0 wire protocol used by
// plugins. Messages are encoded as JSON objects and are framed by a single
// newline on stdio.
package pluginprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// Version is the only JSON-RPC version accepted by this package.
	Version = "2.0"
	// DefaultMaxMessageSize limits a message body to one MiB. The newline used
	// for framing is not counted as part of the body.
	DefaultMaxMessageSize = 1 << 20
)

var (
	ErrFrameTooLarge   = errors.New("pluginprotocol: frame exceeds maximum message size")
	ErrInvalidJSON     = errors.New("pluginprotocol: invalid JSON")
	ErrInvalidRequest  = errors.New("pluginprotocol: invalid request")
	ErrInvalidResponse = errors.New("pluginprotocol: invalid response")
	ErrInvalidMessage  = errors.New("pluginprotocol: invalid message")
)

// Request is a JSON-RPC request. A nil ID means that the request is a
// notification and causes id to be omitted while encoding. IDs may be strings
// or JSON numbers (json.Number is used when decoding).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id,omitempty"`
}

// Notification is a request that deliberately does not have an ID and does
// not receive a response.
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// ErrorObject is the error member of a JSON-RPC response.
type ErrorObject struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Response is a JSON-RPC response. Exactly one of Result and Error must be
// present. Result is raw JSON so that a plugin can return any JSON value.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ErrorObject    `json:"error,omitempty"`
	ID      any             `json:"id"`
}

// Message is the result of decoding one framed JSON-RPC object. Exactly one
// member is non-nil.
type Message struct {
	Request      *Request
	Notification *Notification
	Response     *Response
}

// Compatibility aliases make the JSON-RPC terminology explicit for callers
// that prefer RPC-prefixed type names.
type RPCRequest = Request
type RPCNotification = Notification
type RPCResponse = Response
type RPCError = ErrorObject

func (r Request) Validate() error {
	if r.JSONRPC != Version {
		return fmt.Errorf("%w: jsonrpc must be %q", ErrInvalidRequest, Version)
	}
	if r.Method == "" {
		return fmt.Errorf("%w: method is required", ErrInvalidRequest)
	}
	if err := validateParams(r.Params); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if r.ID != nil {
		if err := validateID(r.ID, false); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
	}
	return nil
}

func (n Notification) Validate() error {
	if n.JSONRPC != Version {
		return fmt.Errorf("%w: jsonrpc must be %q", ErrInvalidRequest, Version)
	}
	if n.Method == "" {
		return fmt.Errorf("%w: method is required", ErrInvalidRequest)
	}
	if err := validateParams(n.Params); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return nil
}

func (e ErrorObject) Validate() error {
	if e.Message == "" {
		return errors.New("error message is required")
	}
	if len(e.Data) != 0 && !json.Valid(e.Data) {
		return errors.New("error data is not valid JSON")
	}
	return nil
}

func (r Response) Validate() error {
	if r.JSONRPC != Version {
		return fmt.Errorf("%w: jsonrpc must be %q", ErrInvalidResponse, Version)
	}
	if (len(r.Result) == 0) == (r.Error == nil) {
		return fmt.Errorf("%w: exactly one of result or error is required", ErrInvalidResponse)
	}
	if len(r.Result) != 0 && !json.Valid(r.Result) {
		return fmt.Errorf("%w: result is not valid JSON", ErrInvalidResponse)
	}
	if r.Error != nil {
		if err := r.Error.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidResponse, err)
		}
	}
	if r.ID != nil {
		if err := validateID(r.ID, true); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidResponse, err)
		}
	}
	return nil
}

func validateParams(params json.RawMessage) error {
	if len(params) == 0 {
		return nil
	}
	if !json.Valid(params) {
		return errors.New("params is not valid JSON")
	}
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return errors.New("params must be an object or array")
	}
	return nil
}

func validateID(id any, response bool) error {
	switch v := id.(type) {
	case string:
		return nil
	case json.Number:
		if v == "" {
			return errors.New("id number is empty")
		}
		if !json.Valid([]byte(v)) {
			return errors.New("id number is invalid")
		}
		return nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return nil
	case nil:
		if response {
			return nil // null is permitted in a response for an unknown ID.
		}
		return errors.New("request id cannot be null")
	default:
		return errors.New("id must be a string or number")
	}
}

// Marshal deterministically encodes one protocol object without a framing
// newline. Struct fields are emitted in protocol order by encoding/json.
func Marshal(v any) ([]byte, error) {
	var value any
	switch x := v.(type) {
	case Request:
		if err := x.Validate(); err != nil {
			return nil, err
		}
		value = x
	case *Request:
		if x == nil {
			return nil, ErrInvalidRequest
		}
		if err := x.Validate(); err != nil {
			return nil, err
		}
		value = x
	case Notification:
		if err := x.Validate(); err != nil {
			return nil, err
		}
		value = x
	case *Notification:
		if x == nil {
			return nil, ErrInvalidRequest
		}
		if err := x.Validate(); err != nil {
			return nil, err
		}
		value = x
	case Response:
		if err := x.Validate(); err != nil {
			return nil, err
		}
		value = x
	case *Response:
		if x == nil {
			return nil, ErrInvalidResponse
		}
		if err := x.Validate(); err != nil {
			return nil, err
		}
		value = x
	default:
		return nil, ErrInvalidMessage
	}
	return json.Marshal(value)
}

func MarshalRequest(r Request) ([]byte, error)           { return Marshal(r) }
func MarshalNotification(n Notification) ([]byte, error) { return Marshal(n) }
func MarshalResponse(r Response) ([]byte, error)         { return Marshal(r) }

// Encode is an alias for Marshal.
func Encode(v any) ([]byte, error) { return Marshal(v) }

// Decode is an alias for DecodeMessage.
func Decode(data []byte) (Message, error) { return DecodeMessage(data) }

// DecodeMessage parses and validates one unframed JSON-RPC object.
func DecodeMessage(data []byte) (Message, error) {
	if !json.Valid(data) {
		return Message{}, fmt.Errorf("%w: %v", ErrInvalidJSON, json.SyntaxError{})
	}
	var raw map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil || raw == nil {
		return Message{}, fmt.Errorf("%w: JSON-RPC value must be an object", ErrInvalidMessage)
	}
	_, hasMethod := raw["method"]
	_, hasResult := raw["result"]
	_, hasError := raw["error"]
	if hasMethod {
		if hasResult || hasError {
			return Message{}, fmt.Errorf("%w: request cannot contain result or error", ErrInvalidMessage)
		}
		var req Request
		if err := unmarshalUseNumber(data, &req); err != nil {
			return Message{}, err
		}
		_, hasID := raw["id"]
		if hasID && bytes.Equal(bytes.TrimSpace(raw["id"]), []byte("null")) {
			return Message{}, fmt.Errorf("%w: request id cannot be null", ErrInvalidRequest)
		}
		if !hasID {
			var n Notification
			if err := unmarshalUseNumber(data, &n); err != nil {
				return Message{}, err
			}
			if err := n.Validate(); err != nil {
				return Message{}, err
			}
			return Message{Notification: &n}, nil
		}
		if err := req.Validate(); err != nil {
			return Message{}, err
		}
		return Message{Request: &req}, nil
	}
	if hasResult || hasError {
		var response Response
		if err := unmarshalUseNumber(data, &response); err != nil {
			return Message{}, err
		}
		if _, ok := raw["id"]; !ok {
			return Message{}, fmt.Errorf("%w: response id is required", ErrInvalidResponse)
		}
		if err := response.Validate(); err != nil {
			return Message{}, err
		}
		return Message{Response: &response}, nil
	}
	if _, hasJSONRPC := raw["jsonrpc"]; hasJSONRPC {
		return Message{}, fmt.Errorf("%w: method is required", ErrInvalidRequest)
	}
	return Message{}, fmt.Errorf("%w: method, result, or error is required", ErrInvalidMessage)
}

func unmarshalUseNumber(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	return nil
}

// DecodeRequest parses an unframed request or notification. Notifications are
// represented by a Request with a nil ID.
func DecodeRequest(data []byte) (Request, error) {
	message, err := DecodeMessage(data)
	if err != nil {
		return Request{}, err
	}
	if message.Request != nil {
		return *message.Request, nil
	}
	if message.Notification != nil {
		return Request{JSONRPC: message.Notification.JSONRPC, Method: message.Notification.Method, Params: message.Notification.Params}, nil
	}
	return Request{}, ErrInvalidRequest
}

func DecodeResponse(data []byte) (Response, error) {
	message, err := DecodeMessage(data)
	if err != nil {
		return Response{}, err
	}
	if message.Response == nil {
		return Response{}, ErrInvalidResponse
	}
	return *message.Response, nil
}
