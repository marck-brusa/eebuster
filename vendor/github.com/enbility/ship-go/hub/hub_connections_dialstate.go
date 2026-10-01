package hub

import (
	"net"
	"sync"
)

// dialState tracks an outgoing connection attempt that has not been registered yet.
//
// SHIP 12.2.2 requires the node with the bigger SKI to keep the most recent connection
// and close all others, and recommends checking for double connections "directly during
// the TLS handshake". When an incoming connection arrives while we are still dialling
// the same SKI, that check has to be able to retire the dial right away — holding the
// raw socket is what makes it synchronous, rather than waiting for the dial to return
// on its own.
type dialState struct {
	mux        sync.Mutex
	conn       net.Conn
	superseded bool
}

func newDialState() *dialState {
	return &dialState{}
}

// adopt hands the raw socket of the current dial attempt to the state so that it can be
// closed on demand. If the dial was already superseded the socket is closed straight
// away, which aborts the TLS handshake that is about to run on it.
//
// establishWebSocketConnection may dial twice (with and without path), so this can be
// called more than once per attempt.
func (d *dialState) adopt(conn net.Conn) {
	d.mux.Lock()
	defer d.mux.Unlock()

	d.conn = conn
	if d.superseded && conn != nil {
		_ = conn.Close()
	}
}

// supersede retires this dial because a more recent connection to the same SKI won.
func (d *dialState) supersede() {
	d.mux.Lock()
	defer d.mux.Unlock()

	d.superseded = true
	if d.conn != nil {
		// closing the socket aborts an in-flight TLS handshake immediately
		_ = d.conn.Close()
	}
}

func (d *dialState) wasSuperseded() bool {
	d.mux.Lock()
	defer d.mux.Unlock()

	return d.superseded
}
