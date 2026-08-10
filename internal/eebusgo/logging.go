package eebusgo

import "strings"

// LogLevel controls how much of ship-go/eebus-go's internal logging reaches the console.
//
// This matters more than it sounds: at trace level every SPINE datagram is dumped as raw JSON
// twice (once per direction), and with a simulated device running that is a continuous
// firehose which completely buries the handful of lines that actually explain a connection
// problem. Levels map onto ship-go's LoggingInterface methods:
//
//	trace -- everything, including the full JSON wire dump and SHIP state transitions
//	debug -- connection attempts and failures, mDNS entries (the useful default)
//	info  -- little more than the local SKI
//	error -- failures only
type LogLevel int

const (
	LogError LogLevel = iota
	LogInfo
	LogDebug
	LogTrace
)

// ParseLogLevel maps a config/flag string to a level, defaulting to debug -- debug keeps
// "trying to connect"/"connection ... failed:" (which are Debugf in ship-go) while dropping
// the trace-level JSON dumps.
func ParseLogLevel(s string) LogLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return LogTrace
	case "info":
		return LogInfo
	case "error", "err":
		return LogError
	default: // "debug" and anything unrecognised
		return LogDebug
	}
}

func (l LogLevel) String() string {
	switch l {
	case LogTrace:
		return "trace"
	case LogInfo:
		return "info"
	case LogError:
		return "error"
	default:
		return "debug"
	}
}

// StdLogger forwards ship-go/eebus-go's internal logging to the standard log package (and
// therefore into the console + the log ring buffer) -- without calling Service.SetLogging,
// these are completely silent (NoLogging is the default), which hides real diagnostics like
// dial attempts and handshake failures. Wire this in wherever a Service is created, not just
// on the primary stack.
//
// The LoggingInterface methods themselves are in logging_methods.go.
type StdLogger struct {
	Prefix string
	Level  LogLevel
	// Observe, if set, receives every formatted message that passes the level filter, before
	// it is logged. Used to scrape ship-go's own dial failures so the real reason a connection
	// failed can be reported back through the API instead of only existing as a log line --
	// see Stack.observeLogLine.
	Observe func(msg string)
}

func (l StdLogger) enabled(level LogLevel) bool { return level <= l.Level }

func (l StdLogger) observe(msg string) {
	if l.Observe != nil {
		l.Observe(msg)
	}
}
