package log

import (
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"personant/internal/clock"
)

// Level controls log verbosity.
type Level uint

const (
	LevelDebug Level = 0
	LevelInfo  Level = 1
	LevelWarn  Level = 2
	LevelError Level = 3
	// LevelNone suppresses all output. It must survive the uint32
	// round-trip through the atomic level word (SetLevel stores uint32);
	// math.MaxInt would truncate on store so GetLevel()==LevelNone would
	// be false afterward. math.MaxUint32 both round-trips cleanly and sits
	// above every real level, preserving suppression semantics.
	LevelNone Level = math.MaxUint32
)

// ValidLevelNames lists the recognized --log-level values in display order.
var ValidLevelNames = []string{"DEBUG", "INFO", "WARN", "ERROR", "NONE"}

// String returns the display name used in log output, e.g. "INFO".
func (l Level) String() string {
	if l > LevelError {
		return ValidLevelNames[len(ValidLevelNames)-1]
	}
	return ValidLevelNames[l]
}

// ParseLevel maps a level name string to a Level. Case-insensitive.
// Valid names are listed in ValidLevelNames.
// Returns (level, true) on success, (LevelInfo, false) on unrecognized input.
func ParseLevel(s string) (Level, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return LevelDebug, true
	case "INFO":
		return LevelInfo, true
	case "WARN":
		return LevelWarn, true
	case "ERROR":
		return LevelError, true
	case "NONE":
		return LevelNone, true
	default:
		return LevelInfo, false
	}
}

// Logger is the interface all call sites depend on.
//
// Caller attribution constraint: when file/line prefixing is enabled the
// implementation resolves the caller with runtime.Caller at a fixed stack
// depth that assumes the emit is reached through the package-level
// forwarders (log.Debug/Info/Warn/Error/Fatal). Calling the concrete
// logger's methods directly (bypassing the forwarders) shifts the stack by
// one frame and mis-attributes the file/line. Route emits through the
// package-level functions.
type Logger interface {
	Debug(format string, args ...any)
	Info(format string, args ...any)
	Warn(format string, args ...any)
	Error(format string, args ...any)
	Fatal(format string, args ...any) // logs at Error level then os.Exit(1)
	SetLevel(level Level)
	GetLevel() Level
	SetShowFileAndLine(show bool)
	GetShowFileAndLine() bool
}

// compactLogger writes <HH:MM:SS>[LEVEL] message lines.
// stderr and file are written independently at their own level thresholds.
// stderrLevel is accessed via atomic ops so SetLevel/GetLevel are race-free
// with concurrent emit calls. mu guards only the writer fields.
type compactLogger struct {
	stderrLevel  atomic.Uint32 // Level stored as uint32; use loadLevel/storeLevel
	showFileLine atomic.Bool   // read on every emit; toggled by SetShowFileAndLine
	mu           sync.Mutex
	stderrW      io.Writer
	fileW        *os.File // nil if no log file
	pathPrefix   string   // repo-root prefix trimmed from caller file paths (with trailing separator)
}

func (c *compactLogger) emit(level Level, format string, args ...any) {
	toStderr := level >= Level(c.stderrLevel.Load())
	toFile := c.fileW != nil // file always gets everything
	if !toStderr && !toFile {
		return
	}
	fileLine := ""
	if c.showFileLine.Load() {
		// Depth 3 assumes emit was reached via a package-level forwarder
		// (see the Logger interface caller-attribution note).
		pc, fileName, lineNum, _ := runtime.Caller(3)
		funcName := runtime.FuncForPC(pc).Name()
		// Trim the repo-root prefix, but only when the caller file
		// actually lives under it. Dependency code and files built in a
		// different directory won't share the prefix; slicing blindly
		// could panic or mis-trim, so fall back to the full path.
		if strings.HasPrefix(fileName, c.pathPrefix) {
			fileName = fileName[len(c.pathPrefix):]
		}
		fileLine = fmt.Sprintf("%s(%d): %s() ", fileName, lineNum, funcName)
	}
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("<%s>[%s] %s%s\n", clock.Profiling().Format("15:04:05"), level.String(), fileLine, msg)
	c.mu.Lock()
	defer c.mu.Unlock()
	if toStderr {
		fmt.Fprint(c.stderrW, line)
	}
	if toFile {
		fmt.Fprint(c.fileW, line)
	}
}

func (c *compactLogger) GetLevel() Level                  { return Level(c.stderrLevel.Load()) }
func (c *compactLogger) GetShowFileAndLine() bool         { return c.showFileLine.Load() }
func (c *compactLogger) SetLevel(level Level)             { c.stderrLevel.Store(uint32(level)) }
func (c *compactLogger) SetShowFileAndLine(show bool)     { c.showFileLine.Store(show) }
func (c *compactLogger) Debug(format string, args ...any) { c.emit(LevelDebug, format, args...) }
func (c *compactLogger) Info(format string, args ...any)  { c.emit(LevelInfo, format, args...) }
func (c *compactLogger) Warn(format string, args ...any)  { c.emit(LevelWarn, format, args...) }
func (c *compactLogger) Error(format string, args ...any) { c.emit(LevelError, format, args...) }
func (c *compactLogger) Fatal(format string, args ...any) {
	c.emit(LevelError, format, args...)
	os.Exit(1)
}

var global atomic.Pointer[compactLogger]
var initOnce sync.Once

func init() {
	cl := &compactLogger{stderrW: os.Stderr}
	cl.stderrLevel.Store(uint32(LevelInfo))
	global.Store(cl)
}

// InitLogger initializes the global logger. Must be called once before any
// goroutines start; subsequent calls are no-ops (sync.Once).
// logPath: if non-empty, all levels are written to this file (created/appended, mode 0644).
// stderrLevel: minimum level emitted to stderr. Use LevelNone to suppress stderr.
func InitLogger(logPath string, stderrLevel Level, logFileLine bool) error {
	var initErr error
	initOnce.Do(func() {
		_, logGoPath, _, _ := runtime.Caller(0)
		logGoPath = path.Dir(path.Dir(path.Dir(logGoPath)))
		cl := &compactLogger{
			stderrW:    os.Stderr,
			pathPrefix: logGoPath + "/",
		}
		cl.stderrLevel.Store(uint32(stderrLevel))
		cl.showFileLine.Store(logFileLine)
		if logPath != "" {
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				initErr = fmt.Errorf("log: open log file: %w", err)
				return
			}
			// Session boundary marker — useful when the file is reused across restarts.
			fmt.Fprintf(f, "new log opened: %s\n", clock.Profiling().Format("2006-01-02 15:04:05"))
			cl.fileW = f
		}
		global.Store(cl)
	})
	return initErr
}

// Package-level forwarding functions — these are the primary call-site API.
func Debug(format string, args ...any) { global.Load().Debug(format, args...) }
func Info(format string, args ...any)  { global.Load().Info(format, args...) }
func Warn(format string, args ...any)  { global.Load().Warn(format, args...) }
func Error(format string, args ...any) { global.Load().Error(format, args...) }
func Fatal(format string, args ...any) { global.Load().Fatal(format, args...) }

func SetLevel(level Level)         { global.Load().SetLevel(level) }
func GetLevel() Level              { return global.Load().GetLevel() }
func SetShowFileAndLine(show bool) { global.Load().SetShowFileAndLine(show) }
func GetShowFileAndLine() bool     { return global.Load().GetShowFileAndLine() }
