// Package tracerfeed streams every SHIP frame to EEBusTracer as it happens, so its deep-dive
// UI can follow a live session instead of importing a file after the fact.
//
// EEBusTracer's two live sources both expect the same line, one per frame:
//
//	[15:04:05.000] SEND to <peer> MSG: <payload>
//	[15:04:05.000] RECV from <peer> MSG: <payload>
//
// It reaches them differently. `capture --tcp host:port` dials us and only reads, so that side
// is a plain TCP server. `capture --target host:port` sends a one-byte datagram first and then
// listens, so that side registers the sender and answers with datagrams.
//
// A tracer that attaches mid-session first gets the frames recorded so far. Most of a session's
// reads happen while it connects; the dashboard's reads are mostly answered from the stack's
// local copy of the device's data, so without the replay a late tracer sees little more than
// heartbeats.
package tracerfeed

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// queueDepth is the per-client backlog. A tracer that cannot keep up loses lines rather than
// stalling frame capture, for the same reason the frame log ignores write errors.
const queueDepth = 256

// replayDepth is how many recent lines a newly attached tracer receives first.
const replayDepth = 5000

type client struct {
	lines chan string
	conn  net.Conn
}

// Feed broadcasts frames to every attached tracer. The zero value is not usable; call New.
type Feed struct {
	mu       sync.Mutex
	tcp      map[*client]struct{}
	udpConn  net.PacketConn
	udpPeers map[string]net.Addr
	closed   bool
	listener net.Listener
	history  []string // the most recent lines, oldest first, at most replayDepth
}

func New() *Feed {
	return &Feed{tcp: map[*client]struct{}{}, udpPeers: map[string]net.Addr{}}
}

// ListenTCP serves the log stream to tracers running `capture --tcp <addr>`.
func (f *Feed) ListenTCP(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err == nil {
		f.mu.Lock()
		f.listener = listener
		f.mu.Unlock()
		go f.accept(listener)
	}
	return err
}

func (f *Feed) accept(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		c := &client{lines: make(chan string, queueDepth), conn: conn}
		// Snapshot and registration under one lock: every later line goes to the queue, so the
		// replay neither misses nor repeats a frame.
		f.mu.Lock()
		replay := append([]string(nil), f.history...)
		f.tcp[c] = struct{}{}
		f.mu.Unlock()
		go f.serve(c, replay)
	}
}

func (f *Feed) serve(c *client, replay []string) {
	defer func() {
		f.mu.Lock()
		delete(f.tcp, c)
		f.mu.Unlock()
		_ = c.conn.Close()
	}()
	for _, line := range replay {
		if _, err := c.conn.Write([]byte(line)); err != nil {
			return
		}
	}
	for line := range c.lines {
		if _, err := c.conn.Write([]byte(line)); err != nil {
			return
		}
	}
}

// ListenUDP answers tracers running `capture --target <addr>`. Any datagram registers its
// sender, which is how that mode announces itself before it starts listening.
func (f *Feed) ListenUDP(addr string) error {
	conn, err := net.ListenPacket("udp", addr)
	if err == nil {
		f.mu.Lock()
		f.udpConn = conn
		f.mu.Unlock()
		go f.register(conn)
	}
	return err
}

func (f *Feed) register(conn net.PacketConn) {
	buf := make([]byte, 64)
	for {
		_, from, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		f.mu.Lock()
		if _, known := f.udpPeers[from.String()]; !known {
			for _, line := range f.history {
				_, _ = conn.WriteTo([]byte(line), from)
			}
			f.udpPeers[from.String()] = from
		}
		f.mu.Unlock()
	}
}

// Publish formats one frame and hands it to every attached tracer. dir is "send" or "recv",
// matching trace.Store.
func (f *Feed) Publish(dir, ski, payload string) {
	direction, preposition := "RECV", "from"
	if dir == "send" {
		direction, preposition = "SEND", "to"
	}
	line := fmt.Sprintf("[%s] %s %s %s MSG: %s\n",
		time.Now().Format("15:04:05.000"), direction, preposition, ski, payload)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.history = append(f.history, line)
	if len(f.history) > replayDepth {
		f.history = f.history[len(f.history)-replayDepth:]
	}
	for c := range f.tcp {
		select {
		case c.lines <- line:
		default: // backlog full: drop this line for this client
		}
	}
	for _, peer := range f.udpPeers {
		if f.udpConn != nil {
			_, _ = f.udpConn.WriteTo([]byte(line), peer)
		}
	}
}

// Close stops listening and disconnects every attached tracer.
func (f *Feed) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		if f.listener != nil {
			_ = f.listener.Close()
		}
		if f.udpConn != nil {
			_ = f.udpConn.Close()
		}
		for c := range f.tcp {
			close(c.lines)
		}
		f.tcp = map[*client]struct{}{}
	}
	return nil
}
