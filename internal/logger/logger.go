package logger

import (
	"fmt"
	"os"
	"time"
)

func write(level, message string) {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	fmt.Fprintf(os.Stderr, "[%s] %s %s\n", timestamp, level, message)
}

func Debug(message string) { write("DEBUG", message) }
func Info(message string)  { write("INFO", message) }
func Warn(message string)  { write("WARN", message) }
func Error(message string) { write("ERROR", message) }

func Infof(format string, args ...any)  { Info(fmt.Sprintf(format, args...)) }
func Warnf(format string, args ...any)  { Warn(fmt.Sprintf(format, args...)) }
func Errorf(format string, args ...any) { Error(fmt.Sprintf(format, args...)) }
