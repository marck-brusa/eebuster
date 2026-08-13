package eebusgo

import (
	"os"
	"strings"
	"testing"
)

// StdLogger.Trace recognises raw frames by the exact argument shape ship-go's websocket layer
// uses. If a vendor update changes that call, frame capture (and with it the whole Trace &
// Conformance feature) silently stops working -- this test turns that silence into a failure.
func TestVendoredFrameLogShape(t *testing.T) {
	source, err := os.ReadFile("../../vendor/github.com/enbility/ship-go/ws/websocket.go")
	if err != nil {
		t.Fatalf("vendored websocket layer not readable: %v", err)
	}
	for _, call := range []string{
		`logging.Log().Trace("Send:", w.remoteSki, text)`,
		`logging.Log().Trace("Recv:", w.remoteSki, text)`,
	} {
		if !strings.Contains(string(source), call) {
			t.Errorf("vendored ship-go no longer logs frames as %q; update StdLogger.Trace's interception", call)
		}
	}
}

func TestTraceInterceptsFrames(t *testing.T) {
	var dirs []string
	var payloads []string
	logger := StdLogger{
		Prefix: "[test]", Level: LogError, // console filter must not matter
		ObserveFrame: func(dir, ski, payload string) {
			dirs = append(dirs, dir+"/"+ski)
			payloads = append(payloads, payload)
		},
	}
	logger.Trace("Send:", "abcd", `{"data":[]}`)
	logger.Trace("Recv:", "abcd", `{"data":[]}`)
	logger.Trace("something", "unrelated") // wrong shape: ignored
	logger.Trace("Send:", 5, "not-a-ski")  // wrong types: ignored

	if len(dirs) != 2 || dirs[0] != "send/abcd" || dirs[1] != "recv/abcd" {
		t.Fatalf("captured %v", dirs)
	}
	if payloads[0] != `{"data":[]}` {
		t.Fatalf("payload = %q", payloads[0])
	}
}
