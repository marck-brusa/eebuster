package eebusgo

import (
	"fmt"
	"sync"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	spinemodel "github.com/enbility/spine-go/model"
)

// Heartbeat timeouts the cadence accepts. The stack's own manager announces 4 s and sends
// every 2 s, which OPEV needs (OPEV-005); a VDE FNN control box sends every 60 s
// (EEBUS_013), the most LPC allows its Energy Guard to announce ([LPC-031]).
const (
	DefaultHeartbeatTimeout = 4 * time.Second
	maxHeartbeatPeriod      = time.Hour
)

// heartbeatFeature is the part of our DeviceDiagnosis server feature the heartbeat writes
// to; setting the data notifies every subscriber.
type heartbeatFeature interface {
	DataCopy(function spinemodel.FunctionType) any
	SetData(function spinemodel.FunctionType, data any)
}

// HeartbeatState is what GET /api/v1/heartbeat reports.
type HeartbeatState struct {
	Running   bool       `json:"running"`
	IntervalS float64    `json:"interval_s"`
	TimeoutS  float64    `json:"timeout_s"`
	Paced     bool       `json:"paced"`
	LastSent  *time.Time `json:"last_sent"`
}

// Heartbeat is the heartbeat of our CEM entity, the one LPC, LPP, OPEV and OSCEV share.
// Until a cadence is set it starts and stops the stack's own heartbeat manager, whose timeout
// is fixed at construction. SetCadence stops that manager for good and sends the heartbeat
// itself, continuing the manager's counter, so the counter never runs backwards.
type Heartbeat struct {
	mu       sync.Mutex
	manager  spineapi.HeartbeatManagerInterface
	feature  heartbeatFeature
	paced    bool
	interval time.Duration
	timeout  time.Duration
	stop     chan struct{}
	lastSent time.Time
}

func newHeartbeat(manager spineapi.HeartbeatManagerInterface, feature heartbeatFeature) *Heartbeat {
	return &Heartbeat{manager: manager, feature: feature, interval: defaultInterval(DefaultHeartbeatTimeout), timeout: DefaultHeartbeatTimeout}
}

// defaultInterval is the stack manager's sending rule: two seconds less than the timeout, so
// a device that treats the timeout as the deadline for the next heartbeat still gets it.
func defaultInterval(timeout time.Duration) time.Duration {
	interval := timeout
	if timeout > 2*time.Second {
		interval = timeout - 2*time.Second
	}
	return interval
}

// Start sends the heartbeat; the first one goes out at once.
func (h *Heartbeat) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.paced {
		_ = h.manager.StartHeartbeat()
	} else if h.stop == nil {
		h.startLocked()
	}
}

// Stop stops sending the heartbeat.
func (h *Heartbeat) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.paced {
		h.manager.StopHeartbeat()
	} else {
		h.stopLocked()
	}
}

// SetCadence sends the heartbeat every interval and announces timeout. interval 0 takes the
// stack manager's rule. A running heartbeat restarts with its first beat at once; a stopped
// one stays stopped.
func (h *Heartbeat) SetCadence(interval, timeout time.Duration) error {
	if interval == 0 {
		interval = defaultInterval(timeout)
	}
	var err error
	switch {
	case h.feature == nil:
		err = fmt.Errorf("no DeviceDiagnosis server feature to send the heartbeat from")
	case timeout < time.Second || timeout > maxHeartbeatPeriod:
		err = fmt.Errorf("timeout %s is outside 1 s to %s", timeout, maxHeartbeatPeriod)
	case interval < time.Second || interval > maxHeartbeatPeriod:
		err = fmt.Errorf("interval %s is outside 1 s to %s", interval, maxHeartbeatPeriod)
	}
	if err == nil {
		h.mu.Lock()
		running := h.stop != nil
		if !h.paced {
			running = h.manager.IsHeartbeatRunning()
			h.manager.StopHeartbeat()
			h.paced = true
		}
		h.stopLocked()
		h.interval, h.timeout = interval, timeout
		if running {
			h.startLocked()
		}
		h.mu.Unlock()
	}
	return err
}

// State reports the cadence and whether the heartbeat is being sent.
func (h *Heartbeat) State() HeartbeatState {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := HeartbeatState{IntervalS: h.interval.Seconds(), TimeoutS: h.timeout.Seconds(), Paced: h.paced, Running: h.stop != nil}
	if !h.paced {
		state.Running = h.manager.IsHeartbeatRunning()
	}
	if !h.lastSent.IsZero() {
		sent := h.lastSent
		state.LastSent = &sent
	}
	return state
}

func (h *Heartbeat) startLocked() {
	stop := make(chan struct{})
	h.stop = stop
	go h.run(stop, h.interval, h.timeout)
}

func (h *Heartbeat) stopLocked() {
	if h.stop != nil {
		close(h.stop)
		h.stop = nil
	}
}

func (h *Heartbeat) run(stop chan struct{}, interval, timeout time.Duration) {
	h.send(stop, timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.send(stop, timeout)
		case <-stop:
			return
		}
	}
}

// send publishes the next heartbeat: the counter of the last one plus one, the current time
// and the announced timeout. A sender that was stopped or replaced in the meantime sends
// nothing, so two senders never publish the same counter.
func (h *Heartbeat) send(stop chan struct{}, timeout time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stop == stop {
		counter := uint64(1)
		last, ok := h.feature.DataCopy(spinemodel.FunctionTypeDeviceDiagnosisHeartbeatData).(*spinemodel.DeviceDiagnosisHeartbeatDataType)
		if ok && last != nil && last.HeartbeatCounter != nil {
			counter = *last.HeartbeatCounter + 1
		}
		now := time.Now().UTC()
		h.feature.SetData(spinemodel.FunctionTypeDeviceDiagnosisHeartbeatData, &spinemodel.DeviceDiagnosisHeartbeatDataType{
			Timestamp:        spinemodel.NewAbsoluteOrRelativeTimeTypeFromTime(now),
			HeartbeatCounter: &counter,
			HeartbeatTimeout: spinemodel.NewDurationType(timeout),
		})
		h.lastSent = now
	}
}
