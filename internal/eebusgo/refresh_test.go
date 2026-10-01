package eebusgo

import (
	"errors"
	"sync"
	"testing"
	"time"

	spineapi "github.com/enbility/spine-go/api"
	spinemodel "github.com/enbility/spine-go/model"
)

// fakeFeature records response callbacks so a test can answer them.
type fakeFeature struct {
	mu        sync.Mutex
	callbacks []func(spineapi.ResponseMessage)
}

func (f *fakeFeature) AddResponseCallback(_ spinemodel.MsgCounterType, cb func(spineapi.ResponseMessage)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callbacks = append(f.callbacks, cb)
	return nil
}

func (f *fakeFeature) answerAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, cb := range f.callbacks {
		cb(spineapi.ResponseMessage{})
	}
}

func sendOK() (*spinemodel.MsgCounterType, error) {
	counter := spinemodel.MsgCounterType(1)
	return &counter, nil
}

func TestRefreshWaitsForEveryReply(t *testing.T) {
	feature := &fakeFeature{}
	go func() {
		time.Sleep(50 * time.Millisecond)
		feature.answerAll()
	}()
	start := time.Now()
	refresh(readRequest{feature: feature, send: sendOK}, readRequest{feature: feature, send: sendOK})
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond || elapsed > time.Second {
		t.Fatalf("refresh returned after %v, want right after the replies (~50 ms)", elapsed)
	}
}

func TestRefreshSkipsReadsThatCannotBeSent(t *testing.T) {
	feature := &fakeFeature{}
	start := time.Now()
	refresh(readRequest{feature: feature, send: func() (*spinemodel.MsgCounterType, error) { return nil, errors.New("not supported") }})
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("refresh waited %v for a read that was never sent", elapsed)
	}
}

func TestRefreshGivesUpAfterTheTimeout(t *testing.T) {
	saved := refreshTimeout
	refreshTimeout = 100 * time.Millisecond
	defer func() { refreshTimeout = saved }()
	start := time.Now()
	refresh(readRequest{feature: &fakeFeature{}, send: sendOK})
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond || elapsed > time.Second {
		t.Fatalf("refresh returned after %v, want the 100 ms timeout", elapsed)
	}
}
