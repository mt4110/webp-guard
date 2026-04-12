package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestFormatProgressLineWhileWalking(t *testing.T) {
	line := formatProgressLine(progressSnapshot{
		Label:     "bulk",
		Total:     24,
		Completed: 9,
		WalkDone:  false,
		Summary: Summary{
			Skipped: 2,
			Failed:  1,
		},
		StartedAt: time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC),
		Now:       time.Date(2026, 4, 12, 12, 0, 5, 0, time.UTC),
	})

	for _, needle := range []string{"bulk walking", "discovered=24", "done=9", "skipped=2", "failed=1", "elapsed=5s"} {
		if !strings.Contains(line, needle) {
			t.Fatalf("expected %q to contain %q", line, needle)
		}
	}
}

func TestFormatProgressLineWithFixedTotal(t *testing.T) {
	line := formatProgressLine(progressSnapshot{
		Label:     "verify",
		Total:     100,
		Completed: 40,
		WalkDone:  true,
		Summary: Summary{
			Skipped: 3,
			Failed:  2,
		},
		StartedAt: time.Date(2026, 4, 12, 12, 0, 0, 0, time.UTC),
		Now:       time.Date(2026, 4, 12, 12, 0, 20, 0, time.UTC),
	})

	for _, needle := range []string{"verify [========............]", "40%", "40/100", "eta=30s", "skipped=3", "failed=2", "elapsed=20s"} {
		if !strings.Contains(line, needle) {
			t.Fatalf("expected %q to contain %q", line, needle)
		}
	}
}

func TestProgressClearForLogClearsRenderedLine(t *testing.T) {
	var buf bytes.Buffer
	progress := &progressReporter{
		writer:    &buf,
		enabled:   true,
		lastWidth: 5,
	}

	progress.ClearForLog()

	if got := buf.String(); got != "\r     \r" {
		t.Fatalf("expected clear sequence, got %q", got)
	}
	if progress.lastWidth != 0 {
		t.Fatalf("expected lastWidth reset, got %d", progress.lastWidth)
	}
}

func TestProgressWriteLineClearsRenderedLineBeforeLog(t *testing.T) {
	var buf bytes.Buffer
	progress := &progressReporter{
		writer:    &buf,
		enabled:   true,
		lastWidth: 5,
	}

	progress.WriteLine(&buf, "record")

	if got := buf.String(); got != "\r     \rrecord\n" {
		t.Fatalf("expected clear sequence followed by log line, got %q", got)
	}
	if progress.lastWidth != 0 {
		t.Fatalf("expected lastWidth reset, got %d", progress.lastWidth)
	}
}
