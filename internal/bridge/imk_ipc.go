package bridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/yeisme/clavis/pkg/core/host"
)

type IMKKeyRequest struct {
	SessionID string            `json:"sessionId"`
	KeyName   string            `json:"keyName"`
	Rune      string            `json:"rune"`
	KeyCode   uint16            `json:"keyCode"`
	Modifiers []string          `json:"modifiers,omitempty"`
	BundleID  string            `json:"bundleId,omitempty"`
	CaretRect host.CursorAnchor `json:"caretRect"`
}
type IMKKeyResponse struct {
	Handled       bool     `json:"handled"`
	MarkedText    string   `json:"markedText,omitempty"`
	CommitText    string   `json:"commitText,omitempty"`
	Candidates    []string `json:"candidates,omitempty"`
	SelectedIndex int      `json:"selectedIndex"`
}

type IMKHandler func(context.Context, IMKKeyRequest) (IMKKeyResponse, error)

type IMKDaemonServer struct {
	listener net.Listener
	handler  IMKHandler
}

func NewIMKDaemonServer(listener net.Listener, handler IMKHandler) *IMKDaemonServer {
	return &IMKDaemonServer{listener: listener, handler: handler}
}
func (s *IMKDaemonServer) Serve(ctx context.Context) error {
	if s.listener == nil {
		return errors.New("imk: nil listener")
	}
	for {
		c, e := s.listener.Accept()
		if e != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return e
			}
		}
		go s.serveConn(ctx, c)
	}
}
func (s *IMKDaemonServer) Close() error {
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}
func (s *IMKDaemonServer) serveConn(ctx context.Context, c net.Conn) {
	defer c.Close()
	r, e := readFrame(c)
	if e != nil {
		return
	}
	var req IMKKeyRequest
	if json.Unmarshal(r, &req) != nil {
		return
	}
	resp := IMKKeyResponse{}
	if s.handler != nil {
		resp, e = s.handler(ctx, req)
		if e != nil {
			resp = IMKKeyResponse{}
		}
	}
	_ = writeFrame(c, resp)
}

type IMKClient struct {
	Address string
	Network string
	Timeout time.Duration
	Dial    func(context.Context, string, string) (net.Conn, error)
}

func (c IMKClient) Call(ctx context.Context, req IMKKeyRequest) (IMKKeyResponse, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dial := c.Dial
	if dial == nil {
		dial = func(ctx context.Context, n, a string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, n, a)
		}
	}
	network := c.Network
	if network == "" {
		network = "unix"
	}
	conn, e := dial(ctx, network, c.Address)
	if e != nil {
		return IMKKeyResponse{}, e
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if e = writeFrame(conn, req); e != nil {
		return IMKKeyResponse{}, e
	}
	data, e := readFrame(conn)
	if e != nil {
		return IMKKeyResponse{}, e
	}
	var resp IMKKeyResponse
	if e = json.Unmarshal(data, &resp); e != nil {
		return IMKKeyResponse{}, fmt.Errorf("imk: decode response: %w", e)
	}
	return resp, nil
}
func writeFrame(w io.Writer, v any) error {
	data, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(data) > 1<<20 {
		return errors.New("imk: frame too large")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(data)))
	if _, e = w.Write(h[:]); e != nil {
		return e
	}
	_, e = w.Write(data)
	return e
}
func readFrame(r io.Reader) ([]byte, error) {
	h := make([]byte, 4)
	if _, e := io.ReadFull(r, h); e != nil {
		return nil, e
	}
	n := binary.BigEndian.Uint32(h)
	if n > 1<<20 {
		return nil, errors.New("imk: frame too large")
	}
	data := make([]byte, n)
	_, e := io.ReadFull(r, data)
	return data, e
}
