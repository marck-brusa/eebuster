package eebusgo

import (
	"sync"
	"time"

	"github.com/enbility/eebus-go/features/client"
	spineapi "github.com/enbility/spine-go/api"
	spinemodel "github.com/enbility/spine-go/model"
)

// refreshTimeout bounds how long a typed read waits for the device's replies. A device that
// does not answer in time is reported from the stack's local copy, as before.
var refreshTimeout = 3 * time.Second

// responder is what every eebus-go feature client offers to follow up on a request.
type responder interface {
	AddResponseCallback(msgCounterReference spinemodel.MsgCounterType, function func(msg spineapi.ResponseMessage)) error
}

// readRequest is one SPINE read on one feature: the feature that follows up on it, and the
// call that sends it.
type readRequest struct {
	feature responder
	send    func() (*spinemodel.MsgCounterType, error)
}

// refresh sends the reads and waits until every reply arrived or refreshTimeout passed.
//
// The stack keeps a local copy of the device's data and answers reads from it, so without
// this a dashboard read never reaches the device: a stale value can be shown, and a tracer
// following the session never sees the read. A read the device does not support, or one
// that fails to send, is skipped.
func refresh(requests ...readRequest) {
	var wg sync.WaitGroup
	for _, r := range requests {
		if r.feature == nil {
			continue
		}
		counter, err := r.send()
		if err != nil || counter == nil {
			continue
		}
		var once sync.Once
		wg.Add(1)
		if r.feature.AddResponseCallback(*counter, func(spineapi.ResponseMessage) { once.Do(wg.Done) }) != nil {
			once.Do(wg.Done)
		}
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(refreshTimeout):
	}
}

// The reads a typed read needs, one builder per data set. Each yields nothing when the remote
// entity has no such feature.

func limitDataRead(local spineapi.EntityLocalInterface, remote spineapi.EntityRemoteInterface) []readRequest {
	lc, err := client.NewLoadControl(local, remote)
	if err != nil {
		return nil
	}
	return []readRequest{{feature: lc, send: func() (*spinemodel.MsgCounterType, error) { return lc.RequestLimitData(nil, nil) }}}
}

func keyValuesRead(local spineapi.EntityLocalInterface, remote spineapi.EntityRemoteInterface) []readRequest {
	dc, err := client.NewDeviceConfiguration(local, remote)
	if err != nil {
		return nil
	}
	return []readRequest{{feature: dc, send: func() (*spinemodel.MsgCounterType, error) { return dc.RequestKeyValues(nil, nil) }}}
}

func measurementDataRead(local spineapi.EntityLocalInterface, remote spineapi.EntityRemoteInterface) []readRequest {
	m, err := client.NewMeasurement(local, remote)
	if err != nil {
		return nil
	}
	return []readRequest{{feature: m, send: func() (*spinemodel.MsgCounterType, error) { return m.RequestData(nil, nil) }}}
}

func permittedValuesRead(local spineapi.EntityLocalInterface, remote spineapi.EntityRemoteInterface) []readRequest {
	ec, err := client.NewElectricalConnection(local, remote)
	if err != nil {
		return nil
	}
	return []readRequest{{feature: ec, send: func() (*spinemodel.MsgCounterType, error) { return ec.RequestPermittedValueSets(nil, nil) }}}
}
