package pluginprotocol

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sync"
)

// Decoder reads newline-delimited JSON-RPC messages. A frame is limited to
// MaxMessageSize bytes, excluding its terminating newline.
type Decoder struct {
	reader *bufio.Reader
	max    int
}

// NewDecoder creates a decoder with DefaultMaxMessageSize.
func NewDecoder(r io.Reader) *Decoder { return NewDecoderSize(r, DefaultMaxMessageSize) }

// NewDecoderSize creates a decoder with an explicit maximum body size.
func NewDecoderSize(r io.Reader, maxMessageSize int) *Decoder {
	if maxMessageSize <= 0 {
		maxMessageSize = DefaultMaxMessageSize
	}
	return &Decoder{reader: bufio.NewReader(r), max: maxMessageSize}
}

// MaxMessageSize reports the configured maximum unframed message size.
func (d *Decoder) MaxMessageSize() int { return d.max }

// Read reads and validates one JSON-RPC message. A final frame without a
// newline is accepted at EOF, which is useful for pipes that close cleanly.
func (d *Decoder) Read() (Message, error) {
	frame, err := d.readFrame()
	if err != nil {
		return Message{}, err
	}
	return DecodeMessage(frame)
}

// Decode is an alias for Read.
func (d *Decoder) Decode() (Message, error) { return d.Read() }

func (d *Decoder) readFrame() ([]byte, error) {
	var frame []byte
	for {
		part, err := d.reader.ReadSlice('\n')
		if len(part) > 0 {
			if len(frame)+len(part) > d.max+1 {
				return nil, ErrFrameTooLarge
			}
			frame = append(frame, part...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		if len(frame) == 0 {
			return nil, io.EOF
		}
		if len(frame) > d.max+1 {
			return nil, ErrFrameTooLarge
		}
		if frame[len(frame)-1] == '\n' {
			frame = frame[:len(frame)-1]
			if len(frame) > 0 && frame[len(frame)-1] == '\r' {
				frame = frame[:len(frame)-1]
			}
		}
		if len(frame) > d.max {
			return nil, ErrFrameTooLarge
		}
		if len(bytes.TrimSpace(frame)) == 0 {
			return nil, fmt.Errorf("%w: empty frame", ErrInvalidJSON)
		}
		return frame, nil
	}
}

// Encoder writes newline-delimited JSON-RPC messages. A single encoder may be
// shared by concurrent plugin calls; each frame is kept contiguous.
type Encoder struct {
	writer io.Writer
	max    int
	mu     sync.Mutex
}

// NewEncoder creates an encoder with DefaultMaxMessageSize.
func NewEncoder(w io.Writer) *Encoder { return NewEncoderSize(w, DefaultMaxMessageSize) }

// NewEncoderSize creates an encoder with an explicit maximum body size.
func NewEncoderSize(w io.Writer, maxMessageSize int) *Encoder {
	if maxMessageSize <= 0 {
		maxMessageSize = DefaultMaxMessageSize
	}
	return &Encoder{writer: w, max: maxMessageSize}
}

// MaxMessageSize reports the configured maximum unframed message size.
func (e *Encoder) MaxMessageSize() int { return e.max }

// Write encodes and writes one complete newline-delimited frame.
func (e *Encoder) Write(v any) error {
	data, err := Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > e.max {
		return ErrFrameTooLarge
	}
	data = append(data, '\n')
	e.mu.Lock()
	defer e.mu.Unlock()
	written, err := e.writer.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

// Encode is an alias for Write.
func (e *Encoder) Encode(v any) error { return e.Write(v) }

// ReadMessage reads one frame from r using the supplied limit.
func ReadMessage(r io.Reader, maxMessageSize int) (Message, error) {
	return NewDecoderSize(r, maxMessageSize).Read()
}

// WriteMessage writes one frame to w using the supplied limit.
func WriteMessage(w io.Writer, v any, maxMessageSize int) error {
	return NewEncoderSize(w, maxMessageSize).Write(v)
}
