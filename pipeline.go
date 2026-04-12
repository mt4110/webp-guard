package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var knownImageExtensions = map[string]string{
	"jpg":  "jpeg",
	"jpeg": "jpeg",
	"png":  "png",
	"webp": "webp",
	"gif":  "gif",
	"svg":  "svg",
	"heic": "heic",
	"heif": "heic",
	"avif": "avif",
}

var supportedInputExtensions = map[string]struct{}{
	"jpg":  {},
	"jpeg": {},
	"png":  {},
}

var alwaysSkippedDirs = map[string]struct{}{
	".git":                      {},
	".hg":                       {},
	".svn":                      {},
	"node_modules":              {},
	"$RECYCLE.BIN":              {},
	"System Volume Information": {},
}

type FileJob struct {
	Path         string
	RelativePath string
	Extension    string
}

type FileRecord struct {
	Timestamp          string              `json:"timestamp"`
	Command            string              `json:"command"`
	Status             string              `json:"status"`
	Path               string              `json:"path"`
	RelativePath       string              `json:"relative_path"`
	ConfigFingerprint  string              `json:"config_fingerprint,omitempty"`
	OutputPath         string              `json:"output_path,omitempty"`
	Extension          string              `json:"extension,omitempty"`
	Magic              string              `json:"magic,omitempty"`
	Width              int                 `json:"width,omitempty"`
	Height             int                 `json:"height,omitempty"`
	OutputWidth        int                 `json:"output_width,omitempty"`
	OutputHeight       int                 `json:"output_height,omitempty"`
	Pixels             int64               `json:"pixels,omitempty"`
	FileSizeBytes      int64               `json:"file_size_bytes,omitempty"`
	OutputSizeBytes    int64               `json:"output_size_bytes,omitempty"`
	SavedBytes         int64               `json:"saved_bytes,omitempty"`
	SavedPercent       float64             `json:"saved_percent,omitempty"`
	Quality            int                 `json:"quality,omitempty"`
	MaxWidth           int                 `json:"max_width,omitempty"`
	Resized            bool                `json:"resized,omitempty"`
	OutputVariants     []OutputVariantInfo `json:"output_variants,omitempty"`
	Orientation        int                 `json:"orientation,omitempty"`
	OrientationApplied bool                `json:"orientation_applied,omitempty"`
	DryRun             bool                `json:"dry_run,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	Error              string              `json:"error,omitempty"`
	DurationMillis     int64               `json:"duration_ms,omitempty"`
}

type Summary struct {
	Command      string         `json:"command"`
	RootDir      string         `json:"root_dir"`
	StartedAt    time.Time      `json:"started_at"`
	FinishedAt   time.Time      `json:"finished_at"`
	Total        int            `json:"total"`
	Converted    int            `json:"converted"`
	Scanned      int            `json:"scanned"`
	DryRun       int            `json:"dry_run"`
	Verified     int            `json:"verified"`
	Skipped      int            `json:"skipped"`
	Rejected     int            `json:"rejected"`
	Failed       int            `json:"failed"`
	Discarded    int            `json:"discarded"`
	StatusCounts map[string]int `json:"status_counts"`
}

func RunProcessCommand(ctx context.Context, cfg ProcessConfig, encoder Encoder, stdout io.Writer) (summary Summary, err error) {
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}

	summary = Summary{
		Command:      string(cfg.Mode),
		RootDir:      cfg.RootDir,
		StartedAt:    time.Now().UTC(),
		StatusCounts: map[string]int{},
	}

	reportWriter, err := reportWriterFactory(cfg.ReportPath)
	if err != nil {
		return Summary{}, err
	}
	defer func() {
		if reportWriter != nil {
			if closeErr := reportWriter.Close(); err == nil && closeErr != nil {
				err = closeErr
			}
		}
	}()

	manifestWriter, err := newManifestWriter(cfg.ManifestPath, cfg)
	if err != nil {
		return Summary{}, err
	}
	defer func() {
		if manifestWriter != nil {
			_ = manifestWriter.Close(summary)
		}
	}()

	resumeSet, err := loadResumeSet(cfg.ResumeFrom, cfg.ConfigFingerprint)
	if err != nil {
		return Summary{}, err
	}

	writef(stdout, "Starting %s on %s (extensions=%s, cpus=%d, workers=%d)\n", cfg.Mode, cfg.RootDir, strings.Join(cfg.ExtensionList, ","), cfg.CPUs, cfg.Workers)

	progress := newProgressReporterWithWriter(stdout, isInteractiveStream(stdout), string(cfg.Mode), 0)
	defer progress.Close()

	jobs := make(chan FileJob, cfg.Workers*2)
	results := make(chan FileRecord, cfg.Workers*2)

	var workerWG sync.WaitGroup
	for i := 0; i < cfg.Workers; i++ {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job, ok := <-jobs:
					if !ok {
						return
					}

					record := processJob(ctx, cfg, job, encoder)
					if err := ctx.Err(); err != nil {
						return
					}

					select {
					case <-ctx.Done():
						return
					case results <- record:
					}
				}
			}
		}()
	}

	walkErrCh := make(chan error, 1)
	go func() {
		walkErrCh <- walkTree(ctx, cfg, jobs, results, resumeSet, progress)
		progress.MarkWalkDone()
		close(jobs)
	}()

	go func() {
		workerWG.Wait()
		close(results)
	}()

	for record := range results {
		summary.Add(record)
		progress.Complete(summary)
		if err := reportWriter.Write(record); err != nil {
			return Summary{}, err
		}
		if manifestWriter != nil && (record.Status == "converted" || record.Status == "converted_partial") {
			if err := manifestWriter.Write(record); err != nil {
				return Summary{}, err
			}
		}
		progress.ClearForLog()
		writeLine(stdout, formatRecord(record))
	}

	if err := <-walkErrCh; err != nil {
		return Summary{}, err
	}

	summary.FinishedAt = time.Now().UTC()
	if manifestWriter != nil {
		if err := manifestWriter.Close(summary); err != nil {
			return Summary{}, err
		}
		manifestWriter = nil
	}
	progress.Finish(summary)

	writeLine(stdout, formatSummary(summary))
	if err := reportWriter.Close(); err != nil {
		return Summary{}, err
	}
	reportWriter = nil
	return summary, nil
}

func (s *Summary) Add(record FileRecord) {
	s.Total++
	s.StatusCounts[record.Status]++

	switch {
	case record.Status == "converted", record.Status == "converted_partial":
		s.Converted++
	case record.Status == "scanned":
		s.Scanned++
	case record.Status == "dry_run":
		s.DryRun++
	case record.Status == "verify_ok":
		s.Verified++
	case record.Status == "discarded_larger":
		s.Discarded++
	case strings.HasPrefix(record.Status, "skipped_"):
		s.Skipped++
	case strings.HasPrefix(record.Status, "rejected_"):
		s.Rejected++
	case strings.HasPrefix(record.Status, "failed_") || strings.HasPrefix(record.Status, "verify_"):
		s.Failed++
	default:
		s.Failed++
	}
}

func (s Summary) ExitCode(mode CommandMode) int {
	if s.Failed > 0 || s.Rejected > 0 {
		if mode == modeVerify {
			return exitVerifyIssue
		}
		return exitScanIssues
	}
	return exitOK
}

func walkTree(ctx context.Context, cfg ProcessConfig, jobs chan<- FileJob, results chan<- FileRecord, resumeSet map[string]struct{}, progress *progressReporter) error {
	root := cfg.RootDir
	stack := []string{root}
	visitedDirs := map[string]struct{}{directoryVisitKey(root, cfg.FollowSymlinks): {}}

	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}

		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := os.ReadDir(dir)
		if err != nil {
			if emitErr := emitWalkResult(ctx, results, newSystemRecord(cfg.Mode, cfg.ConfigFingerprint, "", dir, "failed_walk", "", err), progress); emitErr != nil {
				return emitErr
			}
			continue
		}

		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}

			fullPath := filepath.Join(dir, entry.Name())
			if shouldSkipConfiguredOutputPath(cfg, fullPath) {
				continue
			}
			relPath, err := filepath.Rel(root, fullPath)
			if err != nil {
				if emitErr := emitWalkResult(ctx, results, newSystemRecord(cfg.Mode, cfg.ConfigFingerprint, "", fullPath, "failed_path", "", err), progress); emitErr != nil {
					return emitErr
				}
				continue
			}
			relPath = filepath.ToSlash(relPath)

			if shouldSkipHiddenOrSystem(relPath, entry.Name(), cfg.IncludeHidden) {
				if entry.IsDir() {
					continue
				}
				if isKnownImageExtension(entry.Name()) {
					if emitErr := emitWalkResult(ctx, results, newRecord(cfg.Mode, cfg.ConfigFingerprint, fullPath, relPath, extWithoutDot(entry.Name()), "skipped_hidden"), progress); emitErr != nil {
						return emitErr
					}
				}
				continue
			}

			if matchesAny(cfg.ExcludePatterns, relPath) {
				if entry.IsDir() {
					continue
				}
				if isKnownImageExtension(entry.Name()) {
					record := newRecord(cfg.Mode, cfg.ConfigFingerprint, fullPath, relPath, extWithoutDot(entry.Name()), "skipped_excluded")
					record.Reason = "matched exclude glob"
					if emitErr := emitWalkResult(ctx, results, record, progress); emitErr != nil {
						return emitErr
					}
				}
				continue
			}

			if len(cfg.IncludePatterns) > 0 && !matchesAny(cfg.IncludePatterns, relPath) && !entry.IsDir() {
				if isKnownImageExtension(entry.Name()) {
					record := newRecord(cfg.Mode, cfg.ConfigFingerprint, fullPath, relPath, extWithoutDot(entry.Name()), "skipped_not_included")
					record.Reason = "did not match include glob"
					if emitErr := emitWalkResult(ctx, results, record, progress); emitErr != nil {
						return emitErr
					}
				}
				continue
			}

			if entry.Type()&os.ModeSymlink != 0 {
				if !cfg.FollowSymlinks {
					if isKnownImageExtension(entry.Name()) {
						record := newRecord(cfg.Mode, cfg.ConfigFingerprint, fullPath, relPath, extWithoutDot(entry.Name()), "skipped_symlink")
						record.Reason = "symlink policy blocks traversal by default"
						if emitErr := emitWalkResult(ctx, results, record, progress); emitErr != nil {
							return emitErr
						}
					}
					continue
				}

				resolved, err := filepath.EvalSymlinks(fullPath)
				if err != nil {
					if emitErr := emitWalkResult(ctx, results, newSystemRecord(cfg.Mode, cfg.ConfigFingerprint, relPath, fullPath, "failed_symlink", "", err), progress); emitErr != nil {
						return emitErr
					}
					continue
				}
				insideRoot, err := pathWithinRoot(root, resolved)
				if err != nil {
					if emitErr := emitWalkResult(ctx, results, newSystemRecord(cfg.Mode, cfg.ConfigFingerprint, relPath, fullPath, "failed_symlink", "", err), progress); emitErr != nil {
						return emitErr
					}
					continue
				}
				if !insideRoot {
					record := newRecord(cfg.Mode, cfg.ConfigFingerprint, fullPath, relPath, extWithoutDot(entry.Name()), "rejected_symlink_escape")
					record.Reason = "resolved path leaves the requested root"
					if emitErr := emitWalkResult(ctx, results, record, progress); emitErr != nil {
						return emitErr
					}
					continue
				}

				info, err := os.Stat(fullPath)
				if err != nil {
					if emitErr := emitWalkResult(ctx, results, newSystemRecord(cfg.Mode, cfg.ConfigFingerprint, relPath, fullPath, "failed_stat", "", err), progress); emitErr != nil {
						return emitErr
					}
					continue
				}

				if info.IsDir() {
					visitKey := filepath.Clean(resolved)
					if _, seen := visitedDirs[visitKey]; seen {
						continue
					}
					visitedDirs[visitKey] = struct{}{}
					stack = append(stack, fullPath)
					continue
				}
			}

			if entry.IsDir() {
				if _, skip := alwaysSkippedDirs[entry.Name()]; skip {
					continue
				}
				visitKey := directoryVisitKey(fullPath, cfg.FollowSymlinks)
				if _, seen := visitedDirs[visitKey]; seen {
					continue
				}
				visitedDirs[visitKey] = struct{}{}
				stack = append(stack, fullPath)
				continue
			}

			ext := extWithoutDot(entry.Name())
			_, supported := supportedInputExtensions[ext]
			magicType, known := knownImageExtensions[ext]
			if !known {
				continue
			}
			if !supported {
				record := newRecord(cfg.Mode, cfg.ConfigFingerprint, fullPath, relPath, ext, "rejected_unsupported_format")
				if magicType == "webp" {
					record.Reason = "webp is output-only; supported inputs are png, jpg, and jpeg"
				} else {
					record.Reason = fmt.Sprintf("%s is not supported; supported inputs are png, jpg, and jpeg", ext)
				}
				if emitErr := emitWalkResult(ctx, results, record, progress); emitErr != nil {
					return emitErr
				}
				continue
			}
			if _, ok := cfg.Extensions[ext]; !ok {
				record := newRecord(cfg.Mode, cfg.ConfigFingerprint, fullPath, relPath, ext, "skipped_policy")
				record.Reason = fmt.Sprintf("%s is outside the active extension policy", ext)
				if emitErr := emitWalkResult(ctx, results, record, progress); emitErr != nil {
					return emitErr
				}
				continue
			}
			if _, seen := resumeSet[relPath]; seen {
				record := newRecord(cfg.Mode, cfg.ConfigFingerprint, fullPath, relPath, ext, "skipped_resume")
				record.Reason = "already completed in previous report"
				if emitErr := emitWalkResult(ctx, results, record, progress); emitErr != nil {
					return emitErr
				}
				continue
			}

			if err := emitJob(ctx, jobs, FileJob{
				Path:         fullPath,
				RelativePath: relPath,
				Extension:    ext,
			}, progress); err != nil {
				return err
			}
		}
	}

	return nil
}

func directoryVisitKey(path string, followSymlinks bool) string {
	if !followSymlinks {
		return filepath.Clean(path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(resolved)
}

func emitWalkResult(ctx context.Context, results chan<- FileRecord, record FileRecord, progress *progressReporter) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case results <- record:
		progress.AddTotal(1)
		return nil
	}
}

func emitJob(ctx context.Context, jobs chan<- FileJob, job FileJob, progress *progressReporter) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case jobs <- job:
		progress.AddTotal(1)
		return nil
	}
}

func loadResumeSet(path string, configFingerprint string) (map[string]struct{}, error) {
	set := map[string]struct{}{}
	if strings.TrimSpace(path) == "" {
		return set, nil
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer closeQuietly(file)

		reader := csv.NewReader(file)
		header, err := reader.Read()
		if err != nil {
			return nil, err
		}

		relativeIndex := indexOfCSV(header, "relative_path")
		statusIndex := indexOfCSV(header, "status")
		if relativeIndex < 0 || statusIndex < 0 {
			return nil, fmt.Errorf("resume csv is missing relative_path/status columns")
		}
		fingerprintIndex := indexOfCSV(header, "config_fingerprint")

		for {
			row, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			status := csvValue(row, statusIndex)
			if resumeTerminalStatus(status) && resumeFingerprintMatches(csvValue(row, fingerprintIndex), configFingerprint) {
				set[csvValue(row, relativeIndex)] = struct{}{}
			}
		}
	default:
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer closeQuietly(file)

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var record FileRecord
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				return nil, fmt.Errorf("resume report parse error: %w", err)
			}
			if resumeTerminalStatus(record.Status) &&
				record.RelativePath != "" &&
				resumeFingerprintMatches(record.ConfigFingerprint, configFingerprint) {
				set[record.RelativePath] = struct{}{}
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}

	return set, nil
}

func resumeTerminalStatus(status string) bool {
	switch {
	case status == "converted",
		status == "converted_partial",
		status == "discarded_larger",
		status == "skipped_existing",
		status == "skipped_policy",
		status == "skipped_webp",
		status == "skipped_symlink",
		status == "skipped_hidden",
		status == "skipped_excluded",
		status == "skipped_not_included",
		status == "skipped_resume":
		return true
	case strings.HasPrefix(status, "rejected_"):
		return true
	default:
		return false
	}
}

func formatRecord(record FileRecord) string {
	switch record.Status {
	case "converted", "converted_partial":
		extra := ""
		if len(record.OutputVariants) > 1 {
			extra = fmt.Sprintf(" (+%d variants)", len(record.OutputVariants)-1)
		}
		prefix := "converted"
		if record.Status == "converted_partial" {
			prefix = "converted partial"
		}
		return fmt.Sprintf("[%s] %s -> %s%s (saved %.1f%%)", prefix, record.RelativePath, filepath.Base(record.OutputPath), extra, record.SavedPercent)
	case "dry_run":
		extra := ""
		if len(record.OutputVariants) > 1 {
			extra = fmt.Sprintf(" (+%d variants)", len(record.OutputVariants)-1)
		}
		return fmt.Sprintf("[dry-run] %s -> %s%s", record.RelativePath, filepath.Base(record.OutputPath), extra)
	case "scanned":
		return fmt.Sprintf("[scanned] %s (%dx%d, %s)", record.RelativePath, record.Width, record.Height, record.Magic)
	case "discarded_larger":
		return fmt.Sprintf("[discarded] %s candidate was larger than source", record.RelativePath)
	default:
		message := record.Reason
		if record.Error != "" {
			message = record.Error
		}
		if message == "" {
			return fmt.Sprintf("[%s] %s", record.Status, record.RelativePath)
		}
		return fmt.Sprintf("[%s] %s (%s)", record.Status, record.RelativePath, message)
	}
}

func formatSummary(summary Summary) string {
	duration := summary.FinishedAt.Sub(summary.StartedAt).Round(time.Millisecond)
	return fmt.Sprintf(
		"Summary: total=%d converted=%d scanned=%d dry-run=%d verified=%d skipped=%d rejected=%d failed=%d discarded=%d duration=%s",
		summary.Total,
		summary.Converted,
		summary.Scanned,
		summary.DryRun,
		summary.Verified,
		summary.Skipped,
		summary.Rejected,
		summary.Failed,
		summary.Discarded,
		duration,
	)
}

func newRecord(mode CommandMode, configFingerprint string, path string, relativePath string, ext string, status string) FileRecord {
	return FileRecord{
		Timestamp:         time.Now().UTC().Format(time.RFC3339Nano),
		Command:           string(mode),
		Status:            status,
		Path:              path,
		RelativePath:      relativePath,
		ConfigFingerprint: configFingerprint,
		Extension:         ext,
	}
}

func newSystemRecord(mode CommandMode, configFingerprint string, relativePath string, path string, status string, reason string, err error) FileRecord {
	record := newRecord(mode, configFingerprint, path, relativePath, extWithoutDot(path), status)
	record.Reason = reason
	if err != nil {
		record.Error = err.Error()
	}
	return record
}

func extWithoutDot(name string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
}

func isKnownImageExtension(name string) bool {
	_, ok := knownImageExtensions[extWithoutDot(name)]
	return ok
}

func shouldSkipHiddenOrSystem(relativePath string, name string, includeHidden bool) bool {
	if _, skip := alwaysSkippedDirs[name]; skip {
		return true
	}
	if includeHidden {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	parts := strings.Split(relativePath, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

func pathWithinRoot(root string, candidate string) (bool, error) {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, err
	}
	cleanRel := filepath.Clean(rel)
	if cleanRel == "." {
		return true, nil
	}
	return cleanRel != ".." && !strings.HasPrefix(filepath.ToSlash(cleanRel), "../"), nil
}

func shouldSkipConfiguredOutputPath(cfg ProcessConfig, candidate string) bool {
	if cfg.OutDir == "" {
		return false
	}

	rootPath := cfg.RootDir
	outDirPath := cfg.OutDir
	candidatePath := candidate
	if cfg.FollowSymlinks {
		rootPath = directoryVisitKey(cfg.RootDir, true)
		outDirPath = directoryVisitKey(cfg.OutDir, true)
		candidatePath = directoryVisitKey(candidate, true)
	}

	insideRoot, err := pathWithinRoot(rootPath, outDirPath)
	if err != nil || !insideRoot {
		return false
	}

	withinOutDir, err := pathWithinRoot(outDirPath, candidatePath)
	if err != nil {
		return false
	}
	return withinOutDir
}

func indexOfCSV(header []string, key string) int {
	for i, value := range header {
		if value == key {
			return i
		}
	}
	return -1
}

func csvValue(row []string, index int) string {
	if index < 0 || index >= len(row) {
		return ""
	}
	return row[index]
}

func resumeFingerprintMatches(recordFingerprint string, currentFingerprint string) bool {
	recordFingerprint = strings.TrimSpace(recordFingerprint)
	currentFingerprint = strings.TrimSpace(currentFingerprint)
	return recordFingerprint != "" && currentFingerprint != "" && recordFingerprint == currentFingerprint
}

type processConfigFingerprintPayload struct {
	Schema           int            `json:"schema"`
	RootDir          string         `json:"root_dir"`
	OutDir           string         `json:"out_dir,omitempty"`
	Extensions       []string       `json:"extensions"`
	Include          []string       `json:"include,omitempty"`
	Exclude          []string       `json:"exclude,omitempty"`
	IncludeHidden    bool           `json:"include_hidden"`
	FollowSymlinks   bool           `json:"follow_symlinks"`
	MaxFileSizeBytes int64          `json:"max_file_size_bytes"`
	MaxPixels        int64          `json:"max_pixels"`
	MaxDimension     int            `json:"max_dimension"`
	MaxWidth         int            `json:"max_width"`
	AspectVariants   []string       `json:"aspect_variants,omitempty"`
	CropMode         CropMode       `json:"crop_mode,omitempty"`
	FocusX           float64        `json:"focus_x,omitempty"`
	FocusY           float64        `json:"focus_y,omitempty"`
	Quality          int            `json:"quality"`
	ExistingPolicy   ExistingPolicy `json:"existing_policy"`
}

func processConfigFingerprint(cfg ProcessConfig) string {
	payload := processConfigFingerprintPayload{
		Schema:           1,
		RootDir:          cfg.RootDir,
		OutDir:           cfg.OutDir,
		Extensions:       sortedStrings(cfg.ExtensionList),
		Include:          sortedGlobPatterns(cfg.IncludePatterns),
		Exclude:          sortedGlobPatterns(cfg.ExcludePatterns),
		IncludeHidden:    cfg.IncludeHidden,
		FollowSymlinks:   cfg.FollowSymlinks,
		MaxFileSizeBytes: cfg.MaxFileSizeBytes,
		MaxPixels:        cfg.MaxPixels,
		MaxDimension:     cfg.MaxDimension,
		MaxWidth:         cfg.MaxWidth,
		AspectVariants:   aspectVariantNames(cfg.AspectVariants),
		CropMode:         cfg.CropMode,
		FocusX:           cfg.FocusX,
		FocusY:           cfg.FocusY,
		Quality:          cfg.Quality,
		ExistingPolicy:   cfg.ExistingPolicy,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("process fingerprint marshal failed: %v", err))
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:])
}

func sortedStrings(values []string) []string {
	cloned := append([]string(nil), values...)
	sort.Strings(cloned)
	return cloned
}

func sortedGlobPatterns(patterns []GlobPattern) []string {
	values := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		values = append(values, pattern.Raw)
	}
	sort.Strings(values)
	return values
}
