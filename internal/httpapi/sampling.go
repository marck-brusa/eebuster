package httpapi

import (
	"context"
	"time"
)

// sampleInterval matches the history store's documented cadence: twelve hours at five seconds.
const sampleInterval = 5 * time.Second

// StartSampling records one history sample per connected peer on a fixed interval, until ctx
// is cancelled.
//
// History used to be written only from the snapshot handler, so it existed only while a browser
// was polling it: a backgrounded tab, whose timers the browser throttles to roughly once a
// minute, left ragged gaps in the chart; two open tabs recorded every instant twice; and with
// nobody watching, nothing was recorded at all. Sampling here makes the series uniform and
// independent of who is looking at it.
func (s *Server) StartSampling(ctx context.Context) {
	ticker := time.NewTicker(sampleInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sampleConnectedPeers()
			}
		}
	}()
}

func (s *Server) sampleConnectedPeers() {
	for _, peer := range s.stack.Peers() {
		if peer.Connected {
			snap, err := s.stack.EnergySnapshot(peer.SKI)
			if err == nil {
				s.telemetry.Record(peer.SKI, snapshotSource(snap))
			}
		}
	}
}
