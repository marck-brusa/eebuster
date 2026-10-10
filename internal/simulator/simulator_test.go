package simulator

import (
	"testing"
	"time"

	"github.com/enbility/spine-go/model"
)

// Without the energy guard's heartbeat for more than twice its announced timeout the station
// holds its failsafe consumption limit, whatever limit was written; the heartbeat ends that.
func TestStationFailsafeWithoutGuardHeartbeat(t *testing.T) {
	d := &Device{id: "test", baselineW: 11000, guardTimeout: defaultGuardTimeout, failsafeW: 2000, limitW: 4200, limitActive: true}
	if d.updateFailsafe() {
		t.Error("a station that never saw a guard must not enter failsafe state")
	}
	if limit, active := d.effectiveLimitLocked(); limit != 4200 || !active {
		t.Errorf("effective limit = %v/%v, want the written 4200 W", limit, active)
	}

	timeout := model.NewDurationType(4 * time.Second)
	d.guardHeartbeat(&model.DeviceDiagnosisHeartbeatDataType{HeartbeatTimeout: timeout})
	if d.guardTimeout != 4*time.Second {
		t.Errorf("announced timeout = %v, want 4 s", d.guardTimeout)
	}
	d.mu.Lock()
	d.guardSeen = time.Now().Add(-9 * time.Second)
	d.mu.Unlock()
	if !d.updateFailsafe() || !d.failsafe {
		t.Fatal("9 s without a heartbeat at a 4 s timeout must enter failsafe state")
	}
	if limit, active := d.effectiveLimitLocked(); limit != 2000 || !active {
		t.Errorf("effective limit in failsafe = %v/%v, want 2000 W", limit, active)
	}

	d.guardHeartbeat(&model.DeviceDiagnosisHeartbeatDataType{})
	if !d.updateFailsafe() || d.failsafe {
		t.Error("the heartbeat's return must end the failsafe state")
	}
	if d.guardTimeout != 4*time.Second {
		t.Errorf("a heartbeat without a timeout must keep the announced one, got %v", d.guardTimeout)
	}
}
