package bridge

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestIMKClientServerRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	s := NewIMKDaemonServer(nil, nil)
	_ = s
	go func() {
		defer b.Close()
		data, e := readFrame(b)
		if e != nil {
			return
		}
		var req IMKKeyRequest
		_ = json.Unmarshal(data, &req)
		_ = writeFrame(b, IMKKeyResponse{Handled: true, CommitText: req.Rune})
	}()
	c := IMKClient{Address: "pipe", Network: "pipe", Timeout: time.Second, Dial: func(context.Context, string, string) (net.Conn, error) { return a, nil }}
	got, e := c.Call(context.Background(), IMKKeyRequest{Rune: "a"})
	if e != nil || !got.Handled || got.CommitText != "a" {
		t.Fatalf("got=%+v err=%v", got, e)
	}
}
