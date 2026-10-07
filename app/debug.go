package app

import (
	"io"
	"os"
	"sync/atomic"

	"github.com/rs/zerolog"
	log "github.com/sirupsen/logrus"
)

// bondMinLevel is the minimum zerolog level written for the NDK client.
// The logger itself stays at trace so this filter can move at runtime.
var bondMinLevel atomic.Int32

type bondLevelWriter struct {
	out io.Writer
}

func (w bondLevelWriter) Write(p []byte) (int, error) {
	return w.WriteLevel(zerolog.NoLevel, p)
}

func (w bondLevelWriter) WriteLevel(level zerolog.Level, p []byte) (int, error) {
	if level < zerolog.Level(bondMinLevel.Load()) {
		return len(p), nil
	}
	return w.out.Write(p)
}

// NewBondLogger returns the logger passed to bond. Its level follows SetDebugLogging.
func NewBondLogger() zerolog.Logger {
	bondMinLevel.Store(int32(zerolog.InfoLevel))
	return zerolog.New(bondLevelWriter{out: os.Stderr}).
		Level(zerolog.TraceLevel).
		With().
		Timestamp().
		Logger()
}

// SetDebugLogging turns process and NDK client debug logs on or off.
func SetDebugLogging(enabled bool) {
	if enabled {
		log.SetLevel(log.DebugLevel)
		log.SetReportCaller(true)
		bondMinLevel.Store(int32(zerolog.DebugLevel))
		return
	}
	log.SetLevel(log.InfoLevel)
	log.SetReportCaller(false)
	bondMinLevel.Store(int32(zerolog.InfoLevel))
}
