package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogger_Info(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf)

	log.Info("hello %s", "world")

	output := buf.String()
	if !strings.Contains(output, "[INFO]") {
		t.Error("output missing [INFO]")
	}
	if !strings.Contains(output, "hello world") {
		t.Error("output missing formatted message")
	}
}

func TestLogger_Warn(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf)

	log.Warn("something %s", "happened")

	output := buf.String()
	if !strings.Contains(output, "[WARN]") {
		t.Error("output missing [WARN]")
	}
	if !strings.Contains(output, "something happened") {
		t.Error("output missing formatted message")
	}
}

func TestLogger_Error(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf)

	log.Error("failed: %v", "reason")

	output := buf.String()
	if !strings.Contains(output, "[ERROR]") {
		t.Error("output missing [ERROR]")
	}
	if !strings.Contains(output, "failed: reason") {
		t.Error("output missing formatted message")
	}
}

func TestLogger_Timestamp(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf)

	log.Info("test")

	output := buf.String()
	if !strings.Contains(output, "20") {
		t.Error("output missing timestamp")
	}
}

func TestLogger_Close(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf)

	log.Close()
	log.Close()
}

func TestLogger_New(t *testing.T) {
	dir := t.TempDir()
	logPath := dir + "/subdir/test.log"

	log, err := New(logPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer log.Close()

	log.Info("test")
}
