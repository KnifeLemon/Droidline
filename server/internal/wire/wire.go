// Package wire carries NDJSON lines over TCP, TLS and WebSocket behind one
// interface, and tells protocols apart by the first byte a peer sends.
package wire

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// MaxLine bounds a single incoming line. Envelope parts stay under 512 KiB.
const MaxLine = 1 << 20

var ErrLineTooLong = errors.New("line exceeds 1 MiB")

type LineConn interface {
	ReadLine() ([]byte, error)
	WriteLine([]byte) error
	Close() error
	RemoteAddr() string
	Transport() string
}

// BufConn is a net.Conn whose first bytes may already sit in a bufio.Reader.
type BufConn struct {
	net.Conn
	R *bufio.Reader
}

func (c *BufConn) Read(p []byte) (int, error) { return c.R.Read(p) }

// Sniff waits for the first byte from c without consuming it.
func Sniff(c net.Conn, timeout time.Duration) (byte, *BufConn, error) {
	bc := &BufConn{Conn: c, R: bufio.NewReaderSize(c, 64<<10)}
	if timeout > 0 {
		c.SetReadDeadline(time.Now().Add(timeout))
		defer c.SetReadDeadline(time.Time{})
	}
	b, err := bc.R.Peek(1)
	if err != nil {
		return 0, nil, err
	}
	return b[0], bc, nil
}

type streamConn struct {
	c         net.Conn
	r         *bufio.Reader
	wmu       sync.Mutex
	transport string
}

// NewStream wraps a byte stream. If c is a *BufConn its buffered bytes are kept.
func NewStream(c net.Conn, transport string) LineConn {
	var r *bufio.Reader
	if bc, ok := c.(*BufConn); ok {
		r = bc.R
	} else {
		r = bufio.NewReaderSize(c, 64<<10)
	}
	return &streamConn{c: c, r: r, transport: transport}
}

func (s *streamConn) ReadLine() ([]byte, error) { return ReadLine(s.r) }

func (s *streamConn) WriteLine(b []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.c.SetWriteDeadline(time.Now().Add(30 * time.Second))
	buf := make([]byte, 0, len(b)+1)
	buf = append(append(buf, b...), '\n')
	_, err := s.c.Write(buf)
	return err
}

func (s *streamConn) Close() error       { return s.c.Close() }
func (s *streamConn) RemoteAddr() string { return s.c.RemoteAddr().String() }
func (s *streamConn) Transport() string  { return s.transport }

// ReadLine reads one newline-terminated line, without the newline.
func ReadLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > MaxLine {
			return nil, ErrLineTooLong
		}
		line = append(line, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				return trimEOL(line), nil
			}
			return nil, err
		}
		return trimEOL(line), nil
	}
}

func trimEOL(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		b = b[:n-1]
	}
	return b
}

type wsConn struct {
	c         *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	remote    string
	transport string
	wmu       sync.Mutex
}

// NewWS wraps a WebSocket where each text message is one line.
func NewWS(c *websocket.Conn, remote, transport string) LineConn {
	c.SetReadLimit(MaxLine)
	ctx, cancel := context.WithCancel(context.Background())
	return &wsConn{c: c, ctx: ctx, cancel: cancel, remote: remote, transport: transport}
}

func (w *wsConn) ReadLine() ([]byte, error) {
	typ, b, err := w.c.Read(w.ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, errors.New("binary frames are not part of the protocol")
	}
	return b, nil
}

func (w *wsConn) WriteLine(b []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
	defer cancel()
	return w.c.Write(ctx, websocket.MessageText, b)
}

func (w *wsConn) Close() error {
	w.cancel()
	return w.c.Close(websocket.StatusNormalClosure, "")
}

func (w *wsConn) RemoteAddr() string { return w.remote }
func (w *wsConn) Transport() string  { return w.transport }

// ChanListener hands already-accepted connections to an http.Server.
type ChanListener struct {
	ch     chan net.Conn
	addr   net.Addr
	once   sync.Once
	closed chan struct{}
}

func NewChanListener(addr net.Addr) *ChanListener {
	return &ChanListener{ch: make(chan net.Conn), addr: addr, closed: make(chan struct{})}
}

func (l *ChanListener) Push(c net.Conn) {
	select {
	case l.ch <- c:
	case <-l.closed:
		c.Close()
	}
}

func (l *ChanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *ChanListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *ChanListener) Addr() net.Addr { return l.addr }

// Pipe is an in-memory LineConn pair, used for relay-multiplexed agents and tests.
type Pipe struct {
	in        chan []byte
	peer      *Pipe
	done      chan struct{}
	once      sync.Once
	remote    string
	transport string
}

func NewPipe(remote, transport string) (*Pipe, *Pipe) {
	a := &Pipe{in: make(chan []byte, 256), done: make(chan struct{}), remote: remote, transport: transport}
	b := &Pipe{in: make(chan []byte, 256), done: make(chan struct{}), remote: "pipe", transport: transport}
	a.peer, b.peer = b, a
	return a, b
}

func (p *Pipe) ReadLine() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	case <-p.peer.done:
		select {
		case b := <-p.in:
			return b, nil
		default:
			return nil, io.EOF
		}
	}
}

func (p *Pipe) WriteLine(b []byte) error {
	cp := append([]byte(nil), b...)
	select {
	case p.peer.in <- cp:
		return nil
	case <-p.done:
		return net.ErrClosed
	case <-p.peer.done:
		return net.ErrClosed
	}
}

func (p *Pipe) Close() error {
	p.once.Do(func() { close(p.done) })
	return nil
}

func (p *Pipe) RemoteAddr() string { return p.remote }
func (p *Pipe) Transport() string  { return p.transport }
