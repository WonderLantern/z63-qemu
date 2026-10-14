package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// qmp is a QMP client on QEMU's unix socket: one command at a time, events
// to a callback.
type qmp struct {
	conn    net.Conn
	mu      sync.Mutex // one command in flight
	wmu     sync.Mutex
	replies chan qmpMsg
	closed  chan struct{}
	onEvent func(name string, data json.RawMessage)
	id      int64
}

type qmpMsg struct {
	Return json.RawMessage `json:"return"`
	Error  *struct {
		Class string `json:"class"`
		Desc  string `json:"desc"`
	} `json:"error"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
	ID    int64           `json:"id"`
}

var errQMPClosed = errors.New("QMP connection closed")

// dialQMP connects, retrying while QEMU starts, and negotiates.
func dialQMP(ctx context.Context, path string, onEvent func(string, json.RawMessage)) (*qmp, error) {
	var conn net.Conn
	var err error
	for {
		conn, err = net.Dial("unix", path)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("QMP %s: %w", path, err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	q := &qmp{conn: conn, replies: make(chan qmpMsg, 1), closed: make(chan struct{}), onEvent: onEvent}
	r := bufio.NewReaderSize(conn, 1<<20)
	if _, err := r.ReadBytes('\n'); err != nil { // the greeting
		conn.Close()
		return nil, err
	}
	go q.read(r)
	if _, err := q.cmd("qmp_capabilities", nil); err != nil {
		conn.Close()
		return nil, err
	}
	return q, nil
}

func (q *qmp) read(r *bufio.Reader) {
	defer close(q.closed)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		var m qmpMsg
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m.Event != "" {
			if q.onEvent != nil {
				q.onEvent(m.Event, m.Data)
			}
			continue
		}
		select {
		case q.replies <- m:
		case <-time.After(5 * time.Second): // nobody waits: a timed-out command
		}
	}
}

// cmd runs one command and returns its "return" value.
func (q *qmp) cmd(name string, args any) (json.RawMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.id++
	req := map[string]any{"execute": name, "id": q.id}
	if args != nil {
		req["arguments"] = args
	}
	b, _ := json.Marshal(req)
	q.wmu.Lock()
	_, err := q.conn.Write(append(b, '\n'))
	q.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m := <-q.replies:
			if m.ID != q.id {
				continue // the late reply to a command that timed out
			}
			if m.Error != nil {
				return nil, fmt.Errorf("%s: %s", m.Error.Class, m.Error.Desc)
			}
			return m.Return, nil
		case <-q.closed:
			return nil, errQMPClosed
		case <-timeout:
			return nil, fmt.Errorf("QMP %s: no reply in 10 s", name)
		}
	}
}

func (q *qmp) close() { q.conn.Close() }
