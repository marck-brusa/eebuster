package report

import (
	"fmt"
	"strings"
	"time"
)

// FramesLog renders the wire frames embedded in a run in the EEBus Hub log format, the one
// EEBusTracer imports: local time, direction as seen from the testbench, the peer's SKI and
// the raw payload, one frame per line.
func FramesLog(r *Run) ([]byte, error) {
	var b strings.Builder
	if len(r.Frames) == 0 {
		fmt.Fprintf(&b, "# run %s embeds no wire frames; start a run with include_frames to record them\n", r.ID)
	}
	for _, f := range r.Frames {
		direction := "Recv"
		if frameString(f["dir"]) == "send" {
			direction = "Send"
		}
		fmt.Fprintf(&b, "%s    [%s] %s%s\n", frameTime(f).Format("2006-01-02 15:04:05"), direction, frameString(f["ski"]), frameString(f["raw"]))
	}
	return []byte(b.String()), nil
}

func frameTime(f map[string]any) time.Time {
	return time.Unix(0, int64(frameNumber(f["ts"])*1e9))
}

func frameString(v any) string {
	s, _ := v.(string)
	return s
}

func frameNumber(v any) float64 {
	n, _ := v.(float64)
	return n
}
