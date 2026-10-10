package report

import (
	"bytes"
	"encoding/json"
	"errors"
)

// ErrNoRun is returned for data that holds no run record.
var ErrNoRun = errors.New("the file holds no test run")

// Decode reads a run from its JSON record, or from an HTML report, which embeds the record.
func Decode(data []byte) (*Run, error) {
	const marker = `id="report-data">`
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '<' {
		start := bytes.Index(trimmed, []byte(marker))
		end := -1
		if start >= 0 {
			start += len(marker)
			end = bytes.Index(trimmed[start:], []byte("</script>"))
		}
		if end < 0 {
			trimmed = nil
		} else {
			trimmed = trimmed[start : start+end]
		}
	}
	run := &Run{}
	err := json.Unmarshal(trimmed, run)
	if err != nil || run.Schema == 0 || run.ID == "" {
		run, err = nil, ErrNoRun
	}
	return run, err
}
