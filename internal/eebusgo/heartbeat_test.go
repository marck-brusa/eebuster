package eebusgo

import (
	"sync"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	spinemodel "github.com/enbility/spine-go/model"
)

type fakeHeartbeatManager struct {
	mu      sync.Mutex
	running bool
}

func (m *fakeHeartbeatManager) IsHeartbeatRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *fakeHeartbeatManager) SetLocalFeature(spineapi.EntityLocalInterface, spineapi.FeatureLocalInterface) {
}

func (m *fakeHeartbeatManager) StartHeartbeat() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = true
	return nil
}

func (m *fakeHeartbeatManager) StopHeartbeat() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = false
}

// fakeHeartbeatFeature records every heartbeat set on it.
type fakeHeartbeatFeature struct {
	mu   sync.Mutex
	last *spinemodel.DeviceDiagnosisHeartbeatDataType
	sent []*spinemodel.DeviceDiagnosisHeartbeatDataType
}

func (f *fakeHeartbeatFeature) DataCopy(spinemodel.FunctionType) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

func (f *fakeHeartbeatFeature) SetData(_ spinemodel.FunctionType, data any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = data.(*spinemodel.DeviceDiagnosisHeartbeatDataType)
	f.sent = append(f.sent, f.last)
}

func (f *fakeHeartbeatFeature) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeHeartbeatFeature) waitFor(t *testing.T, n int) []*spinemodel.DeviceDiagnosisHeartbeatDataType {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.count() < n && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) < n {
		t.Fatalf("%d heartbeat(s) sent, want %d", len(f.sent), n)
	}
	return append([]*spinemodel.DeviceDiagnosisHeartbeatDataType{}, f.sent...)
}

func TestHeartbeatDelegatesToTheStackUntilACadenceIsSet(t *testing.T) {
	manager, feature := &fakeHeartbeatManager{}, &fakeHeartbeatFeature{}
	h := newHeartbeat(manager, feature)
	h.Start()
	if state := h.State(); !state.Running || state.Paced || state.TimeoutS != 4 || state.IntervalS != 2 {
		t.Errorf("after start: %+v", state)
	}
	h.Stop()
	if manager.IsHeartbeatRunning() || h.State().Running {
		t.Error("stop did not reach the stack's manager")
	}
	if feature.count() != 0 {
		t.Errorf("the stack's manager sends the heartbeat, not the pacer; %d sent", feature.count())
	}
}

func TestHeartbeatCadenceContinuesTheCounterAndAnnouncesTheTimeout(t *testing.T) {
	counter := uint64(41)
	manager := &fakeHeartbeatManager{running: true}
	feature := &fakeHeartbeatFeature{last: &spinemodel.DeviceDiagnosisHeartbeatDataType{HeartbeatCounter: &counter}}
	h := newHeartbeat(manager, feature)
	if err := h.SetCadence(0, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	if manager.IsHeartbeatRunning() {
		t.Error("the stack's manager still runs next to the pacer")
	}
	sent := feature.waitFor(t, 1)
	timeout, _ := sent[0].HeartbeatTimeout.GetTimeDuration()
	if *sent[0].HeartbeatCounter != 42 || timeout != 60*time.Second || sent[0].Timestamp == nil {
		t.Errorf("first heartbeat: counter %d, timeout %s", *sent[0].HeartbeatCounter, timeout)
	}
	if state := h.State(); !state.Running || !state.Paced || state.IntervalS != 58 || state.TimeoutS != 60 || state.LastSent == nil {
		t.Errorf("state: %+v", state)
	}

	// A shorter interval restarts the running heartbeat; the counter keeps counting up.
	if err := h.SetCadence(time.Second, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	sent = feature.waitFor(t, 3)
	for i := 1; i < len(sent); i++ {
		if *sent[i].HeartbeatCounter != *sent[i-1].HeartbeatCounter+1 {
			t.Fatalf("counter not consecutive: %d after %d", *sent[i].HeartbeatCounter, *sent[i-1].HeartbeatCounter)
		}
	}

	h.Stop()
	stopped := feature.count()
	time.Sleep(1500 * time.Millisecond)
	if feature.count() != stopped || h.State().Running {
		t.Errorf("heartbeats after stop: %d, then %d", stopped, feature.count())
	}
	// Restoring the default keeps the pacer, so the counter never falls back to the manager's.
	if err := h.SetCadence(0, DefaultHeartbeatTimeout); err != nil || h.State().Running || manager.IsHeartbeatRunning() {
		t.Errorf("default cadence while stopped: err %v, state %+v", err, h.State())
	}
	h.Start()
	feature.waitFor(t, stopped+1)
	h.Stop()
}

func TestHeartbeatCadenceRejectsOutOfRangeValues(t *testing.T) {
	h := newHeartbeat(&fakeHeartbeatManager{}, &fakeHeartbeatFeature{})
	for _, c := range []struct{ interval, timeout time.Duration }{
		{0, 500 * time.Millisecond}, {time.Second, 2 * time.Hour}, {2 * time.Hour, 60 * time.Second},
	} {
		if err := h.SetCadence(c.interval, c.timeout); err == nil {
			t.Errorf("interval %s, timeout %s accepted", c.interval, c.timeout)
		}
	}
	if h.State().Paced {
		t.Error("a rejected cadence took over from the stack's manager")
	}
	if err := newHeartbeat(&fakeHeartbeatManager{}, nil).SetCadence(0, time.Minute); err == nil {
		t.Error("a cadence without a DeviceDiagnosis feature was accepted")
	}
}
