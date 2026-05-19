package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Logger struct {
	file   *os.File
	writer io.Writer
}

func New(logPath string) (*Logger, error) {
	dir := filepath.Dir(logPath)
	if dir != "" && dir != "/" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	return &Logger{
		file:   f,
		writer: io.MultiWriter(os.Stdout, f),
	}, nil
}

func NewWithWriter(w io.Writer) *Logger {
	return &Logger{
		writer: w,
	}
}

func (l *Logger) Info(format string, args ...interface{}) {
	l.write("INFO", format, args...)
}

func (l *Logger) Warn(format string, args ...interface{}) {
	l.write("WARN", format, args...)
}

func (l *Logger) Error(format string, args ...interface{}) {
	l.write("ERROR", format, args...)
}

func (l *Logger) Close() {
	if l.file != nil {
		l.file.Close()
	}
}

func (l *Logger) write(level, format string, args ...interface{}) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf("[%s] [%s] %s\n", timestamp, level, fmt.Sprintf(format, args...))
	fmt.Fprint(l.writer, msg)
}
