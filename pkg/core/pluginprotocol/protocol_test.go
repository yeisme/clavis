package pluginprotocol

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRequestRoundTripPreservesNumberID(t *testing.T) {
	request := Request{JSONRPC: Version, Method: "sum", Params: []byte(`{"values":[1,2]}`), ID: "request-1"}
	encoded, err := Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"jsonrpc":"2.0","method":"sum","params":{"values":[1,2]},"id":"request-1"}`; got != want {
		t.Fatalf("encoded request = %s, want %s", got, want)
	}
	message, err := DecodeMessage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if message.Request == nil || message.Request.Method != request.Method || message.Request.ID != request.ID {
		t.Fatalf("decoded request = %#v", message.Request)
	}
	if string(message.Request.Params) != string(request.Params) {
		t.Fatalf("decoded params = %s, want %s", message.Request.Params, request.Params)
	}
}

func TestValidNumericRequest(t *testing.T) {
	message, err := DecodeMessage([]byte(`{"jsonrpc":"2.0","method":"echo","id":7}`))
	if err != nil {
		t.Fatal(err)
	}
	if message.Request == nil {
		t.Fatal("expected request")
	}
	if got := message.Request.ID.(jsonNumberStringer); got.String() != "7" {
		t.Fatalf("id = %v, want 7", got)
	}
}

// json.Number is intentionally checked through its small interface to keep
// this test focused on the wire representation rather than implementation type.
type jsonNumberStringer interface{ String() string }

func TestMalformedJSON(t *testing.T) {
	_, err := DecodeMessage([]byte(`{"jsonrpc":"2.0","method":`))
	if !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("error = %v, want ErrInvalidJSON", err)
	}
}

func TestRejectsInvalidRequests(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"jsonrpc":"1.0","method":"x","id":1}`),
		[]byte(`{"jsonrpc":"2.0","id":1}`),
		[]byte(`{"jsonrpc":"2.0","method":"x","params":true,"id":1}`),
		[]byte(`{"jsonrpc":"2.0","method":"x","id":null}`),
	}
	for _, data := range cases {
		if _, err := DecodeMessage(data); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("DecodeMessage(%s) error = %v, want ErrInvalidRequest", data, err)
		}
	}
}

func TestNotificationHasNoID(t *testing.T) {
	message, err := DecodeMessage([]byte(`{"jsonrpc":"2.0","method":"initialized","params":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if message.Notification == nil || message.Request != nil || message.Response != nil {
		t.Fatalf("message = %#v, want notification", message)
	}
	encoded, err := Marshal(*message.Notification)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"id"`) {
		t.Fatalf("notification unexpectedly has id: %s", encoded)
	}
}

func TestErrorResponse(t *testing.T) {
	data := []byte(`{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found","data":{"method":"missing"}},"id":3}`)
	message, err := DecodeMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	if message.Response == nil || message.Response.Error == nil {
		t.Fatalf("message = %#v, want error response", message)
	}
	if message.Response.Error.Code != -32601 || message.Response.Error.Message != "Method not found" {
		t.Fatalf("error = %#v", message.Response.Error)
	}
	if _, err := Marshal(Response{JSONRPC: Version, Result: []byte(`null`), Error: &ErrorObject{Code: 1, Message: "both"}, ID: 3}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("expected mutually-exclusive result/error error, got %v", err)
	}
}

func TestNewlineDelimitedRoundTrip(t *testing.T) {
	var stream bytes.Buffer
	encoder := NewEncoder(&stream)
	if err := encoder.Write(Request{JSONRPC: Version, Method: "one", ID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Write(Notification{JSONRPC: Version, Method: "event"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(stream.String(), "\n"); got != 2 {
		t.Fatalf("newline count = %d, want 2", got)
	}
	decoder := NewDecoder(&stream)
	first, err := decoder.Read()
	if err != nil || first.Request == nil {
		t.Fatalf("first = %#v, err = %v", first, err)
	}
	second, err := decoder.Read()
	if err != nil || second.Notification == nil {
		t.Fatalf("second = %#v, err = %v", second, err)
	}
	if _, err := decoder.Read(); !errors.Is(err, io.EOF) {
		t.Fatalf("third read error = %v, want EOF", err)
	}
}

func TestOversizedFrames(t *testing.T) {
	var stream bytes.Buffer
	if err := NewEncoderSize(&stream, 20).Write(Request{JSONRPC: Version, Method: "too-long", ID: 1}); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("encode error = %v, want ErrFrameTooLarge", err)
	}
	decoder := NewDecoderSize(strings.NewReader(`{"jsonrpc":"2.0","method":"too-long","id":1}`+"\n"), 20)
	if _, err := decoder.Read(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("decode error = %v, want ErrFrameTooLarge", err)
	}
}

func TestResponseRoundTrip(t *testing.T) {
	response := Response{JSONRPC: Version, Result: []byte(`{"ok":true}`), ID: 42}
	encoded, err := MarshalResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeResponse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded.Result) != string(response.Result) || decoded.ID.(jsonNumberStringer).String() != "42" {
		t.Fatalf("decoded = %#v", decoded)
	}
}
