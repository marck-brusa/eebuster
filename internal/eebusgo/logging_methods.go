package eebusgo

import (
	"fmt"
	"log"
)

// The LoggingInterface implementations live here rather than next to the type so each one can
// route through a single pair of helpers that apply the level filter, offer the text to
// Observe, and then log.
//
// Note the Observe hook deliberately runs only for messages that pass the filter: the dial
// failures it scrapes are Debug/Error level, so they survive every level except "error"-only,
// and there is no reason to pay for formatting suppressed trace spam.
func (l StdLogger) emit(level LogLevel, args ...interface{}) {
	if !l.enabled(level) {
		return
	}
	msg := l.Prefix + " " + fmt.Sprint(args...)
	l.observe(msg)
	log.Print(msg)
}

func (l StdLogger) emitf(level LogLevel, format string, args ...interface{}) {
	if !l.enabled(level) {
		return
	}
	msg := l.Prefix + " " + fmt.Sprintf(format, args...)
	l.observe(msg)
	log.Print(msg)
}

func (l StdLogger) Trace(args ...interface{})                 { l.emit(LogTrace, args...) }
func (l StdLogger) Tracef(format string, args ...interface{}) { l.emitf(LogTrace, format, args...) }
func (l StdLogger) Debug(args ...interface{})                 { l.emit(LogDebug, args...) }
func (l StdLogger) Debugf(format string, args ...interface{}) { l.emitf(LogDebug, format, args...) }
func (l StdLogger) Info(args ...interface{})                  { l.emit(LogInfo, args...) }
func (l StdLogger) Infof(format string, args ...interface{})  { l.emitf(LogInfo, format, args...) }
func (l StdLogger) Error(args ...interface{})                 { l.emit(LogError, args...) }
func (l StdLogger) Errorf(format string, args ...interface{}) { l.emitf(LogError, format, args...) }
