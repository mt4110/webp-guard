package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type reportWriter interface {
	Write(FileRecord) error
	Close() error
}

type noopReportWriter struct{}

func (noopReportWriter) Write(FileRecord) error { return nil }
func (noopReportWriter) Close() error           { return nil }

type jsonlReportWriter struct {
	file   *os.File
	writer *bufio.Writer
}

type csvReportWriter struct {
	file   *os.File
	writer *csv.Writer
}

type manifestWriter struct {
	file       *os.File
	writer     *bufio.Writer
	first      bool
	closed     bool
	sourceRoot string
	outputRoot string
	rootDirRel string
	command    CommandMode
	generated  string
}

type ManifestEntry struct {
	RelativePath       string              `json:"relative_path"`
	SourcePath         string              `json:"source_path"`
	OutputPath         string              `json:"output_path"`
	SourceSizeBytes    int64               `json:"source_size_bytes"`
	OutputSizeBytes    int64               `json:"output_size_bytes"`
	Width              int                 `json:"width"`
	Height             int                 `json:"height"`
	OutputWidth        int                 `json:"output_width"`
	OutputHeight       int                 `json:"output_height"`
	Quality            int                 `json:"quality"`
	MaxWidth           int                 `json:"max_width,omitempty"`
	Resized            bool                `json:"resized"`
	OutputVariants     []OutputVariantInfo `json:"variants,omitempty"`
	Orientation        int                 `json:"orientation"`
	OrientationApplied bool                `json:"orientation_applied"`
	SavedBytes         int64               `json:"saved_bytes"`
	SavedPercent       float64             `json:"saved_percent"`
}

func newReportWriter(path string) (reportWriter, error) {
	if strings.TrimSpace(path) == "" {
		return noopReportWriter{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		writer := csv.NewWriter(file)
		header := []string{
			"timestamp",
			"command",
			"status",
			"path",
			"relative_path",
			"config_fingerprint",
			"output_path",
			"extension",
			"magic",
			"width",
			"height",
			"output_width",
			"output_height",
			"pixels",
			"file_size_bytes",
			"output_size_bytes",
			"saved_bytes",
			"saved_percent",
			"quality",
			"max_width",
			"resized",
			"orientation",
			"orientation_applied",
			"dry_run",
			"reason",
			"error",
			"duration_ms",
		}
		if err := writer.Write(header); err != nil {
			_ = file.Close()
			return nil, err
		}
		return &csvReportWriter{file: file, writer: writer}, nil
	default:
		return &jsonlReportWriter{file: file, writer: bufio.NewWriter(file)}, nil
	}
}

func (w *jsonlReportWriter) Write(record FileRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := w.writer.Write(encoded); err != nil {
		return err
	}
	return w.writer.WriteByte('\n')
}

func (w *jsonlReportWriter) Close() error {
	if err := w.writer.Flush(); err != nil {
		_ = w.file.Close()
		return err
	}
	return w.file.Close()
}

func (w *csvReportWriter) Write(record FileRecord) error {
	row := []string{
		record.Timestamp,
		record.Command,
		record.Status,
		record.Path,
		record.RelativePath,
		record.ConfigFingerprint,
		record.OutputPath,
		record.Extension,
		record.Magic,
		fmt.Sprintf("%d", record.Width),
		fmt.Sprintf("%d", record.Height),
		fmt.Sprintf("%d", record.OutputWidth),
		fmt.Sprintf("%d", record.OutputHeight),
		fmt.Sprintf("%d", record.Pixels),
		fmt.Sprintf("%d", record.FileSizeBytes),
		fmt.Sprintf("%d", record.OutputSizeBytes),
		fmt.Sprintf("%d", record.SavedBytes),
		fmt.Sprintf("%.2f", record.SavedPercent),
		fmt.Sprintf("%d", record.Quality),
		fmt.Sprintf("%d", record.MaxWidth),
		fmt.Sprintf("%t", record.Resized),
		fmt.Sprintf("%d", record.Orientation),
		fmt.Sprintf("%t", record.OrientationApplied),
		fmt.Sprintf("%t", record.DryRun),
		record.Reason,
		record.Error,
		fmt.Sprintf("%d", record.DurationMillis),
	}
	return w.writer.Write(row)
}

func (w *csvReportWriter) Close() error {
	w.writer.Flush()
	if err := w.writer.Error(); err != nil {
		_ = w.file.Close()
		return err
	}
	return w.file.Close()
}

func newManifestWriter(path string, cfg ProcessConfig) (*manifestWriter, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	writer := bufio.NewWriter(file)
	generated := timeNowRFC3339()
	manifestDir := filepath.Dir(path)
	outputRoot := cfg.RootDir
	if cfg.OutDir != "" {
		outputRoot = cfg.OutDir
	}
	sourceRootRel, err := relativeArtifactPath(manifestDir, cfg.RootDir)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	outputRootRel, err := relativeArtifactPath(manifestDir, outputRoot)
	if err != nil {
		_ = file.Close()
		return nil, err
	}

	if _, err := fmt.Fprintf(
		writer,
		"{\n  \"version\": 1,\n  \"generated_at\": %q,\n  \"command\": %q,\n  \"root_dir\": %q,\n  \"output_dir\": %q,\n  \"entries\": [\n",
		generated,
		cfg.Mode,
		sourceRootRel,
		outputRootRel,
	); err != nil {
		_ = file.Close()
		return nil, err
	}

	return &manifestWriter{
		file:       file,
		writer:     writer,
		first:      true,
		sourceRoot: cfg.RootDir,
		outputRoot: outputRoot,
		rootDirRel: sourceRootRel,
		command:    cfg.Mode,
		generated:  generated,
	}, nil
}

func (w *manifestWriter) Write(record FileRecord) error {
	sourcePath, err := relativeArtifactPath(w.sourceRoot, record.Path)
	if err != nil {
		return err
	}
	outputVariants := make([]OutputVariantInfo, 0, len(record.OutputVariants))
	for _, variant := range record.OutputVariants {
		outputPath, err := relativeArtifactPath(w.outputRoot, variant.OutputPath)
		if err != nil {
			return err
		}
		outputVariants = append(outputVariants, OutputVariantInfo{
			Name:            variant.Name,
			Usage:           variant.Usage,
			AspectRatio:     variant.AspectRatio,
			OutputPath:      outputPath,
			OutputWidth:     variant.OutputWidth,
			OutputHeight:    variant.OutputHeight,
			OutputSizeBytes: variant.OutputSizeBytes,
			SavedBytes:      variant.SavedBytes,
			SavedPercent:    variant.SavedPercent,
			Resized:         variant.Resized,
			Cropped:         variant.Cropped,
		})
	}
	primaryVariant, ok := primaryOutputVariant(outputVariants)
	if !ok {
		return fmt.Errorf("record for %s is missing a primary output variant", record.RelativePath)
	}

	entry := ManifestEntry{
		RelativePath:       record.RelativePath,
		SourcePath:         sourcePath,
		OutputPath:         primaryVariant.OutputPath,
		SourceSizeBytes:    record.FileSizeBytes,
		OutputSizeBytes:    primaryVariant.OutputSizeBytes,
		Width:              record.Width,
		Height:             record.Height,
		OutputWidth:        primaryVariant.OutputWidth,
		OutputHeight:       primaryVariant.OutputHeight,
		Quality:            record.Quality,
		MaxWidth:           record.MaxWidth,
		Resized:            primaryVariant.Resized,
		OutputVariants:     outputVariants,
		Orientation:        record.Orientation,
		OrientationApplied: record.OrientationApplied,
		SavedBytes:         primaryVariant.SavedBytes,
		SavedPercent:       primaryVariant.SavedPercent,
	}

	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	if !w.first {
		if _, err := w.writer.WriteString(",\n"); err != nil {
			return err
		}
	}
	w.first = false

	_, err = w.writer.WriteString("    " + string(encoded))
	return err
}

func (w *manifestWriter) Close(summary Summary) error {
	if w == nil || w.closed {
		return nil
	}
	w.closed = true
	summary.RootDir = w.rootDirRel

	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		_ = w.file.Close()
		return err
	}

	if _, err := w.writer.WriteString("\n  ],\n  \"summary\": "); err != nil {
		_ = w.file.Close()
		return err
	}
	if _, err := w.writer.Write(summaryJSON); err != nil {
		_ = w.file.Close()
		return err
	}
	if _, err := w.writer.WriteString("\n}\n"); err != nil {
		_ = w.file.Close()
		return err
	}
	if err := w.writer.Flush(); err != nil {
		_ = w.file.Close()
		return err
	}
	return w.file.Close()
}

func timeNowRFC3339() string {
	return timeNow().UTC().Format(time.RFC3339)
}

var timeNow = func() time.Time {
	return time.Now()
}
