package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type progressSnapshot struct {
	Label     string
	Total     int
	Completed int
	WalkDone  bool
	Summary   Summary
	StartedAt time.Time
	Now       time.Time
}

type progressReporter struct {
	mu         sync.Mutex
	writer     io.Writer
	enabled    bool
	label      string
	total      int
	completed  int
	walkDone   bool
	startedAt  time.Time
	summary    Summary
	lastRender time.Time
	lastWidth  int
	finished   bool
}

func newProgressReporter(label string, total int) *progressReporter {
	return newProgressReporterWithWriter(os.Stderr, isInteractiveStream(os.Stderr), label, total)
}

func newProgressReporterWithWriter(writer io.Writer, enabled bool, label string, total int) *progressReporter {
	return &progressReporter{
		writer:    writer,
		enabled:   enabled,
		label:     label,
		total:     total,
		startedAt: time.Now(),
	}
}

func isInteractiveStream(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func (p *progressReporter) AddTotal(delta int) {
	if delta <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.total += delta
	p.renderLocked(false)
}

func (p *progressReporter) Complete(summary Summary) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.completed = summary.Total
	p.summary = summary
	p.renderLocked(false)
}

func (p *progressReporter) MarkWalkDone() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.walkDone = true
	p.renderLocked(true)
}

func (p *progressReporter) Finish(summary Summary) {
	if !p.enabled {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.finished {
		return
	}
	p.completed = summary.Total
	p.summary = summary
	p.walkDone = true
	p.renderLocked(true)
	_, _ = fmt.Fprint(p.writer, "\n")
	p.finished = true
}

func (p *progressReporter) Close() {
	if !p.enabled {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.finished {
		return
	}
	if p.lastWidth > 0 {
		_, _ = fmt.Fprint(p.writer, "\n")
	}
	p.finished = true
}

func (p *progressReporter) ClearForLog() {
	if !p.enabled {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.clearLocked()
}

func (p *progressReporter) WriteLine(writer io.Writer, args ...any) {
	if !p.enabled || writer != p.writer {
		writeLine(writer, args...)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.clearLocked()
	writeLine(writer, args...)
}

func (p *progressReporter) renderLocked(force bool) {
	if !p.enabled || p.finished {
		return
	}
	now := time.Now()
	if !force && !p.lastRender.IsZero() && now.Sub(p.lastRender) < 100*time.Millisecond {
		return
	}

	line := formatProgressLine(progressSnapshot{
		Label:     p.label,
		Total:     p.total,
		Completed: p.completed,
		WalkDone:  p.walkDone,
		Summary:   p.summary,
		StartedAt: p.startedAt,
		Now:       now,
	})
	if line == "" {
		return
	}

	padding := ""
	if p.lastWidth > len(line) {
		padding = strings.Repeat(" ", p.lastWidth-len(line))
	}
	_, _ = fmt.Fprintf(p.writer, "\r%s%s", line, padding)
	p.lastWidth = len(line)
	p.lastRender = now
}

func (p *progressReporter) clearLocked() {
	if p.finished || p.lastWidth == 0 {
		return
	}
	_, _ = fmt.Fprintf(p.writer, "\r%s\r", strings.Repeat(" ", p.lastWidth))
	p.lastWidth = 0
	p.lastRender = time.Time{}
}

func formatProgressLine(snapshot progressSnapshot) string {
	label := strings.TrimSpace(snapshot.Label)
	if label == "" {
		label = "work"
	}

	elapsed := snapshot.Now.Sub(snapshot.StartedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	elapsedText := elapsed.Round(time.Second).String()
	if elapsedText == "0s" && elapsed > 0 {
		elapsedText = "<1s"
	}

	if snapshot.WalkDone {
		if snapshot.Total == 0 {
			return fmt.Sprintf("%s no matching files elapsed=%s", label, elapsedText)
		}

		completed := snapshot.Completed
		if completed > snapshot.Total {
			completed = snapshot.Total
		}
		percent := float64(completed) / float64(snapshot.Total)
		if percent < 0 {
			percent = 0
		}
		if percent > 1 {
			percent = 1
		}

		etaText := "--"
		if completed > 0 && completed < snapshot.Total && elapsed > 0 {
			rate := float64(completed) / elapsed.Seconds()
			if rate > 0 {
				remaining := time.Duration(float64(snapshot.Total-completed)/rate) * time.Second
				etaText = remaining.Round(time.Second).String()
			}
		}
		if completed == snapshot.Total {
			etaText = "0s"
		}

		return fmt.Sprintf(
			"%s %s %3.0f%% %d/%d eta=%s skipped=%d failed=%d elapsed=%s",
			label,
			renderProgressBar(percent, 20),
			percent*100,
			completed,
			snapshot.Total,
			etaText,
			snapshot.Summary.Skipped,
			snapshot.Summary.Failed,
			elapsedText,
		)
	}

	return fmt.Sprintf(
		"%s walking discovered=%d done=%d skipped=%d failed=%d elapsed=%s",
		label,
		snapshot.Total,
		snapshot.Completed,
		snapshot.Summary.Skipped,
		snapshot.Summary.Failed,
		elapsedText,
	)
}

func renderProgressBar(percent float64, width int) string {
	if width <= 0 {
		return ""
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 1 {
		percent = 1
	}

	filled := int(percent * float64(width))
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("=", filled) + strings.Repeat(".", width-filled) + "]"
}
