package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type chunkReader struct {
	chunks    [][]byte
	afterRead func(index int)
	reads     int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}

	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	r.reads++
	if r.afterRead != nil {
		r.afterRead(r.reads)
	}
	return copy(p, chunk), nil
}

func TestCopyWithContextStopsBetweenChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &chunkReader{
		chunks: [][]byte{
			[]byte("abc"),
			[]byte("def"),
		},
		afterRead: func(index int) {
			if index == 1 {
				cancel()
			}
		},
	}

	var dst bytes.Buffer
	written, err := copyWithContext(ctx, &dst, reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if written != 3 {
		t.Fatalf("expected only the first chunk to be written, got %d", written)
	}
	if dst.String() != "abc" {
		t.Fatalf("unexpected copied data %q", dst.String())
	}
}

func TestWriteJSONFileHonorsCanceledContext(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "deploy-plan.json")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := writeJSONFile(ctx, target, map[string]any{"schema_version": 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("expected target file to be absent, got err=%v", statErr)
	}

	tempFiles, globErr := filepath.Glob(filepath.Join(dir, ".webp-guard-json-*"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(tempFiles) != 0 {
		t.Fatalf("expected no staged json temp files, got %v", tempFiles)
	}
}

func TestRunPlanHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RunPlan(ctx, PlanConfig{}, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
