// Package client talks to a running server's client API over NDJSON.
// The CLI and the MCP adapter use it; it mirrors what the SDKs do.
package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

var ErrNotRunning = errors.New("no Droidline server is running here; start one with: droidline serve")

type Client struct {
	conn    net.Conn
	wmu     sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan map[string]any
	nextID  atomic.Int64
	Events  chan map[string]any
	closed  chan struct{}
	err     error
}

func Dial(addr, token string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, ErrNotRunning
		}
		return nil, err
	}
	c := &Client{conn: conn, pending: map[int64]chan map[string]any{},
		Events: make(chan map[string]any, 256), closed: make(chan struct{})}
	go c.read()
	if token != "" {
		r, err := c.Call(map[string]any{"cmd": "auth", "token": token})
		if err != nil {
			conn.Close()
			return nil, err
		}
		if ok, _ := r["ok"].(bool); !ok {
			conn.Close()
			return nil, fmt.Errorf("%v", r["msg"])
		}
	}
	return c, nil
}

func (c *Client) read() {
	r := bufio.NewReaderSize(c.conn, 1<<20)
	defer close(c.closed)
	for {
		line, err := readBig(r)
		if err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			return
		}
		var m map[string]any
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if _, isEvent := m["event"]; isEvent {
			select {
			case c.Events <- m:
			default:
			}
			continue
		}
		id, _ := m["id"].(float64)
		c.mu.Lock()
		ch := c.pending[int64(id)]
		delete(c.pending, int64(id))
		c.mu.Unlock()
		if ch != nil {
			delete(m, "id")
			ch <- m
		}
	}
}

// readBig reads one line of any size; screenshot responses can be several MiB.
func readBig(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := r.ReadSlice('\n')
		out = append(out, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return out[:len(out)-1], nil
	}
}

// Call sends one request and waits for its response.
func (c *Client) Call(req map[string]any) (map[string]any, error) {
	id := c.nextID.Add(1)
	ch := make(chan map[string]any, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	msg := make(map[string]any, len(req)+1)
	for k, v := range req {
		msg[k] = v
	}
	msg["id"] = id
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	c.wmu.Lock()
	_, err = c.conn.Write(append(b, '\n'))
	c.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r, nil
	case <-c.closed:
		c.mu.Lock()
		defer c.mu.Unlock()
		return nil, fmt.Errorf("server closed the connection: %v", c.err)
	}
}

func (c *Client) Close() error { return c.conn.Close() }
