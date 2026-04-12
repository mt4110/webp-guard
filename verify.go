package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/image/webp"
)

var webpDimensionsDecoder = decodeWebPDimensions

func RunVerify(ctx context.Context, cfg VerifyConfig, stdout io.Writer) (Summary, error) {
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}

	previousGOMAXPROCS := runtime.GOMAXPROCS(cfg.CPUs)
	defer runtime.GOMAXPROCS(previousGOMAXPROCS)

	reportWriter, err := newReportWriter(cfg.ReportPath)
	if err != nil {
		return Summary{}, err
	}
	defer func() {
		_ = reportWriter.Close()
	}()

	summary := Summary{
		Command:      string(modeVerify),
		RootDir:      cfg.RootDir,
		StartedAt:    time.Now().UTC(),
		StatusCounts: map[string]int{},
	}

	writef(stdout, "Starting verify on %s using %s (cpus=%d)\n", cfg.RootDir, cfg.ManifestPath, cfg.CPUs)

	manifest, err := readConversionManifest(cfg.ManifestPath)
	if err != nil {
		return Summary{}, err
	}
	sourceRoot, err := resolveConversionManifestSourceRoot(manifest, cfg.RootDir)
	if err != nil {
		return Summary{}, err
	}
	outputRoot, err := resolveConversionManifestOutputRoot(manifest)
	if err != nil {
		return Summary{}, err
	}

	progress := newProgressReporterWithWriter(stdout, isInteractiveStream(stdout), string(modeVerify), len(manifest.Entries))
	defer progress.Close()
	progress.MarkWalkDone()

	for _, entry := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return Summary{}, err
		}

		sourcePath, pathErr := resolveArtifactPath(sourceRoot, entry.SourcePath)
		record := newRecord(modeVerify, "", entry.SourcePath, entry.RelativePath, extWithoutDot(entry.SourcePath), "")
		record.OutputPath = entry.OutputPath
		record.FileSizeBytes = entry.SourceSizeBytes
		record.OutputSizeBytes = entry.OutputSizeBytes
		record.Width = entry.Width
		record.Height = entry.Height
		record.OutputWidth = entry.OutputWidth
		record.OutputHeight = entry.OutputHeight
		record.Quality = entry.Quality
		record.Orientation = entry.Orientation
		record.OrientationApplied = entry.OrientationApplied
		record.Resized = entry.Resized
		record.OutputVariants = manifestEntryOutputVariants(entry)

		if pathErr != nil {
			record.Status = "verify_missing_source"
			record.Error = pathErr.Error()
		} else {
			record.Path = sourcePath
		}

		info, err := os.Stat(sourcePath)
		if pathErr != nil {
			// path resolution already failed, keep that as the primary error
		} else if err != nil {
			record.Status = "verify_missing_source"
			record.Error = err.Error()
		} else if info.Size() != entry.SourceSizeBytes {
			record.Status = "verify_source_changed"
			record.Reason = fmt.Sprintf("source size changed from %d to %d", entry.SourceSizeBytes, info.Size())
		} else {
			record.Status = "verify_ok"
			for index, variant := range record.OutputVariants {
				outputPath, resolveErr := resolveArtifactPath(outputRoot, variant.OutputPath)
				if resolveErr != nil {
					record.Status = "verify_missing_output"
					record.Error = decorateVerifyVariantError(variant, resolveErr.Error())
					break
				}
				record.OutputVariants[index].OutputPath = outputPath

				outputInfo, outErr := os.Stat(outputPath)
				if outErr != nil {
					record.Status = "verify_missing_output"
					record.Error = decorateVerifyVariantError(variant, outErr.Error())
					break
				}
				if outputInfo.Size() > info.Size() {
					record.Status = "verify_size_regression"
					record.Reason = decorateVerifyVariantError(variant, fmt.Sprintf("output %d is larger than source %d", outputInfo.Size(), info.Size()))
					break
				}

				width, height, decodeErr := webpDimensionsDecoder(outputPath)
				if decodeErr != nil {
					record.Status = "verify_decode_failed"
					record.Error = decorateVerifyVariantError(variant, decodeErr.Error())
					break
				}
				if width != variant.OutputWidth || height != variant.OutputHeight {
					record.Status = "verify_dimension_mismatch"
					record.Reason = decorateVerifyVariantError(variant, fmt.Sprintf("manifest=%dx%d actual=%dx%d", variant.OutputWidth, variant.OutputHeight, width, height))
					break
				}
				if width > cfg.MaxWidth {
					record.Status = "verify_max_width_exceeded"
					record.Reason = decorateVerifyVariantError(variant, fmt.Sprintf("output width %d exceeds limit %d", width, cfg.MaxWidth))
					break
				}
			}
		}

		summary.Add(record)
		progress.Complete(summary)
		if err := reportWriter.Write(record); err != nil {
			return Summary{}, err
		}
		writeLine(stdout, formatRecord(record))
	}

	summary.FinishedAt = time.Now().UTC()
	progress.Finish(summary)
	writeLine(stdout, formatSummary(summary))
	return summary, nil
}

func decodeWebPDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer closeQuietly(file)

	cfg, err := webp.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

func decorateVerifyVariantError(variant OutputVariantInfo, message string) string {
	label := variant.AspectRatio
	if strings.TrimSpace(label) == "" {
		label = "primary"
	}
	return fmt.Sprintf("%s variant: %s", label, message)
}
