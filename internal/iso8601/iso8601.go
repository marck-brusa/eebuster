// Package iso8601 parses and formats the PnDTnHnMnS subset of ISO-8601 durations used by the
// REST API for LPC/LPP limit/failsafe durations. Matches src/facade/api/iso8601.py exactly --
// weeks/months/years are deliberately unsupported, LPC/LPP durations are always sub-day in
// practice.
package iso8601

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var durationRe = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?)?$`)

// Parse converts e.g. "PT2H", "PT30M", "PT1M30S" to a time.Duration.
func Parse(value string) (time.Duration, error) {
	m := durationRe.FindStringSubmatch(value)
	if m == nil || (m[1] == "" && m[2] == "" && m[3] == "" && m[4] == "") {
		return 0, fmt.Errorf("unsupported ISO-8601 duration: %q (expected PnDTnHnMnS form)", value)
	}
	var total time.Duration
	if m[1] != "" {
		days, _ := strconv.Atoi(m[1])
		total += time.Duration(days) * 24 * time.Hour
	}
	if m[2] != "" {
		hours, _ := strconv.Atoi(m[2])
		total += time.Duration(hours) * time.Hour
	}
	if m[3] != "" {
		minutes, _ := strconv.Atoi(m[3])
		total += time.Duration(minutes) * time.Minute
	}
	if m[4] != "" {
		seconds, _ := strconv.ParseFloat(m[4], 64)
		total += time.Duration(seconds * float64(time.Second))
	}
	return total, nil
}

// Format converts a time.Duration back to PnDTnHnMnS form, e.g. time.Hour -> "PT1H".
func Format(d time.Duration) string {
	totalSeconds := int64(d.Seconds())
	days := totalSeconds / 86400
	totalSeconds %= 86400
	hours := totalSeconds / 3600
	totalSeconds %= 3600
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60

	out := "P"
	if days > 0 {
		out += fmt.Sprintf("%dD", days)
	}
	timePart := ""
	if hours > 0 {
		timePart += fmt.Sprintf("%dH", hours)
	}
	if minutes > 0 {
		timePart += fmt.Sprintf("%dM", minutes)
	}
	if seconds > 0 {
		timePart += fmt.Sprintf("%dS", seconds)
	}
	if timePart != "" {
		out += "T" + timePart
	}
	if out == "P" {
		return "PT0S"
	}
	return out
}
