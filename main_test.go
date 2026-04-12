package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type fakeEncoder struct {
	check  func() error
	encode func(inputPath string, outputPath string, quality int) error
}

func (f fakeEncoder) Check() error {
	if f.check != nil {
		return f.check()
	}
	return nil
}

func (f fakeEncoder) Encode(_ context.Context, inputPath string, outputPath string, quality int) error {
	return f.encode(inputPath, outputPath, quality)
}

func stubWebPDimensions(t *testing.T, fn func(path string) (int, int, error)) {
	t.Helper()

	originalDecoder := webpDimensionsDecoder
	webpDimensionsDecoder = fn
	t.Cleanup(func() {
		webpDimensionsDecoder = originalDecoder
	})
}

func stubReportWriterFactory(t *testing.T, fn func(path string) (reportWriter, error)) {
	t.Helper()

	originalFactory := reportWriterFactory
	reportWriterFactory = fn
	t.Cleanup(func() {
		reportWriterFactory = originalFactory
	})
}

type stubReportWriter struct {
	writeErr error
	closeErr error
}

func (w *stubReportWriter) Write(FileRecord) error {
	return w.writeErr
}

func (w *stubReportWriter) Close() error {
	return w.closeErr
}

func newDimensionAwareFakeEncoder(t *testing.T, size int, hook func(inputPath string, outputPath string, quality int) error) fakeEncoder {
	t.Helper()

	var (
		mu   sync.Mutex
		dims = map[string]image.Config{}
	)
	stubWebPDimensions(t, func(path string) (int, int, error) {
		mu.Lock()
		defer mu.Unlock()

		cfg, ok := dims[path]
		if !ok {
			return 0, 0, fmt.Errorf("unexpected webp path %s", path)
		}
		return cfg.Width, cfg.Height, nil
	})

	return fakeEncoder{
		encode: func(inputPath string, outputPath string, quality int) error {
			if hook != nil {
				if err := hook(inputPath, outputPath, quality); err != nil {
					return err
				}
			}

			file, err := os.Open(inputPath)
			if err != nil {
				return err
			}
			defer closeQuietly(file)

			cfg, _, err := image.DecodeConfig(file)
			if err != nil {
				return err
			}

			mu.Lock()
			dims[outputPath] = cfg
			mu.Unlock()

			return os.WriteFile(outputPath, bytes.Repeat([]byte("w"), size), 0o644)
		},
	}
}

func TestBulkRejectsMagicMismatch(t *testing.T) {
	root := t.TempDir()
	pngPath := filepath.Join(root, "broken.jpg")
	writePNG(t, pngPath, 10, 10)

	cfg := testConfig(root)
	record := processJob(context.Background(), cfg, FileJob{
		Path:         pngPath,
		RelativePath: "broken.jpg",
		Extension:    "jpg",
	}, fakeEncoder{})

	if record.Status != "rejected_magic_mismatch" {
		t.Fatalf("expected rejected_magic_mismatch, got %s", record.Status)
	}
}

func TestParseExtensionsRejectsUnsupportedFormats(t *testing.T) {
	_, _, err := parseExtensions("png,avif")
	if err == nil {
		t.Fatal("expected unsupported format to be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported extension") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBulkDownsizesWithoutUpscaling(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hero.jpg")
	writeJPEG(t, source, 2400, 1200)

	cfg := testConfig(root)
	cfg.Quality = 82

	var encoded image.Config
	record := processJob(context.Background(), cfg, FileJob{
		Path:         source,
		RelativePath: "hero.jpg",
		Extension:    "jpg",
	}, newDimensionAwareFakeEncoder(t, 128, func(inputPath string, outputPath string, quality int) error {
		file, err := os.Open(inputPath)
		if err != nil {
			return err
		}
		defer closeQuietly(file)

		cfg, err := png.DecodeConfig(file)
		if err == nil {
			encoded = image.Config{Width: cfg.Width, Height: cfg.Height}
		}

		return nil
	}))

	if record.Status != "converted" {
		t.Fatalf("expected converted, got %s (%s)", record.Status, record.Error)
	}
	if !record.Resized {
		t.Fatalf("expected resize to occur")
	}
	if record.OutputWidth != 1200 {
		t.Fatalf("expected output width 1200, got %d", record.OutputWidth)
	}
	if encoded.Width != 1200 || encoded.Height != 600 {
		t.Fatalf("expected temp input 1200x600, got %dx%d", encoded.Width, encoded.Height)
	}

	small := filepath.Join(root, "thumb.jpg")
	writeJPEG(t, small, 800, 400)
	record = processJob(context.Background(), cfg, FileJob{
		Path:         small,
		RelativePath: "thumb.jpg",
		Extension:    "jpg",
	}, newDimensionAwareFakeEncoder(t, 64, func(inputPath string, outputPath string, quality int) error {
		if strings.HasSuffix(inputPath, ".png") {
			t.Fatalf("did not expect temp resize input for small image")
		}
		return nil
	}))

	if record.Status != "converted" {
		t.Fatalf("expected converted, got %s", record.Status)
	}
	if record.Resized {
		t.Fatalf("did not expect resize for image under max-width")
	}
}

func TestCropImageToAspectSupportsSafeAndFocusModes(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 300, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
		for x := 100; x < 200; x++ {
			img.SetNRGBA(x, y, color.NRGBA{G: 255, A: 255})
		}
		for x := 200; x < 300; x++ {
			img.SetNRGBA(x, y, color.NRGBA{B: 255, A: 255})
		}
	}

	safeCrop, safeChanged := cropImageToAspect(img, 1, 1, cropModeSafe, 1.0, 0.5)
	if !safeChanged {
		t.Fatal("expected safe crop to crop a wide image")
	}
	if safeCrop.Bounds().Dx() != 100 || safeCrop.Bounds().Dy() != 100 {
		t.Fatalf("expected safe crop to produce 100x100, got %dx%d", safeCrop.Bounds().Dx(), safeCrop.Bounds().Dy())
	}
	safeCenter := color.NRGBAModel.Convert(safeCrop.At(50, 50)).(color.NRGBA)
	if safeCenter.G < 200 {
		t.Fatalf("expected safe crop to stay centered on green band, got %#v", safeCenter)
	}

	focusCrop, focusChanged := cropImageToAspect(img, 1, 1, cropModeFocus, 1.0, 0.5)
	if !focusChanged {
		t.Fatal("expected focus crop to crop a wide image")
	}
	if focusCrop.Bounds().Dx() != 100 || focusCrop.Bounds().Dy() != 100 {
		t.Fatalf("expected focus crop to produce 100x100, got %dx%d", focusCrop.Bounds().Dx(), focusCrop.Bounds().Dy())
	}
	focusCenter := color.NRGBAModel.Convert(focusCrop.At(50, 50)).(color.NRGBA)
	if focusCenter.B < 200 {
		t.Fatalf("expected focus crop to follow the blue band, got %#v", focusCenter)
	}
}

func TestBulkGeneratesAspectVariants(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hero.jpg")
	writeJPEG(t, source, 2400, 1200)

	aspectVariants, err := parseAspectVariants("16:9,4:3,1:1")
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(root)
	cfg.AspectVariants = aspectVariants
	cfg.CropMode = cropModeSafe

	record := processJob(context.Background(), cfg, FileJob{
		Path:         source,
		RelativePath: "hero.jpg",
		Extension:    "jpg",
	}, newDimensionAwareFakeEncoder(t, 128, nil))

	if record.Status != "converted" {
		t.Fatalf("expected converted, got %s (%s)", record.Status, record.Error)
	}
	if len(record.OutputVariants) != 3 {
		t.Fatalf("expected 3 output variants, got %#v", record.OutputVariants)
	}
	if record.OutputPath != filepath.Join(root, "hero.webp") {
		t.Fatalf("expected primary output path to stay unsuffixed, got %s", record.OutputPath)
	}
	if record.OutputWidth != 1200 || record.OutputHeight != 675 {
		t.Fatalf("expected primary variant to be 1200x675, got %dx%d", record.OutputWidth, record.OutputHeight)
	}

	expected := map[string]image.Point{
		filepath.Join(root, "hero.webp"):     {X: 1200, Y: 675},
		filepath.Join(root, "hero.4x3.webp"): {X: 1200, Y: 900},
		filepath.Join(root, "hero.1x1.webp"): {X: 1200, Y: 1200},
	}
	for _, variant := range record.OutputVariants {
		dims, ok := expected[variant.OutputPath]
		if !ok {
			t.Fatalf("unexpected output variant path %s", variant.OutputPath)
		}
		if variant.OutputWidth != dims.X || variant.OutputHeight != dims.Y {
			t.Fatalf("expected %s to be %dx%d, got %dx%d", variant.OutputPath, dims.X, dims.Y, variant.OutputWidth, variant.OutputHeight)
		}
		if _, err := os.Stat(variant.OutputPath); err != nil {
			t.Fatalf("expected generated variant %s: %v", variant.OutputPath, err)
		}
	}
}

func TestBulkRejectsBrokenGeneratedWebP(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hero.jpg")
	writeJPEG(t, source, 1200, 600)

	stubWebPDimensions(t, func(path string) (int, int, error) {
		return 0, 0, fmt.Errorf("broken webp")
	})

	record := processJob(context.Background(), testConfig(root), FileJob{
		Path:         source,
		RelativePath: "hero.jpg",
		Extension:    "jpg",
	}, fakeEncoder{
		encode: func(inputPath string, outputPath string, quality int) error {
			return os.WriteFile(outputPath, bytes.Repeat([]byte("w"), 64), 0o644)
		},
	})

	if record.Status != "failed_decode_output" {
		t.Fatalf("expected failed_decode_output, got %s (%s)", record.Status, record.Error)
	}
}

func TestBulkRejectsDimensionMismatchAfterEncode(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hero.jpg")
	writeJPEG(t, source, 1200, 600)

	stubWebPDimensions(t, func(path string) (int, int, error) {
		return 640, 320, nil
	})

	record := processJob(context.Background(), testConfig(root), FileJob{
		Path:         source,
		RelativePath: "hero.jpg",
		Extension:    "jpg",
	}, fakeEncoder{
		encode: func(inputPath string, outputPath string, quality int) error {
			return os.WriteFile(outputPath, bytes.Repeat([]byte("w"), 64), 0o644)
		},
	})

	if record.Status != "failed_output_mismatch" {
		t.Fatalf("expected failed_output_mismatch, got %s (%s)", record.Status, record.Reason)
	}
}

func TestScanReportsCorruptImage(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad.png")
	if err := os.WriteFile(bad, []byte("not-a-png"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(root)
	cfg.Mode = modeScan

	record := processJob(context.Background(), cfg, FileJob{
		Path:         bad,
		RelativePath: "bad.png",
		Extension:    "png",
	}, fakeEncoder{})

	if record.Status != "rejected_unknown_magic" && record.Status != "failed_decode_config" {
		t.Fatalf("expected scan failure, got %s", record.Status)
	}
}

func TestReadJPEGOrientation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "oriented.jpg")
	writeJPEGWithOrientation(t, path, 4, 3, 6)

	orientation, err := readJPEGOrientation(path)
	if err != nil {
		t.Fatal(err)
	}
	if orientation != 6 {
		t.Fatalf("expected orientation 6, got %d", orientation)
	}
}

func TestVerifyReadsManifest(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sample.jpg")
	outputDir := filepath.Join(root, "generated")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(outputDir, "sample.webp")
	writeJPEG(t, source, 800, 400)
	if err := os.WriteFile(output, []byte("webp"), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}

	manifest := filepath.Join(root, "manifest.json")
	manifestContent := `{
  "version": 1,
  "generated_at": "2026-04-10T00:00:00Z",
  "command": "bulk",
  "root_dir": ".",
  "output_dir": "generated",
  "entries": [
    {
      "relative_path": "sample.jpg",
      "source_path": "sample.jpg",
      "output_path": "sample.webp",
      "source_size_bytes": ` + strconv.FormatInt(sourceInfo.Size(), 10) + `,
      "output_size_bytes": 4,
      "width": 800,
      "height": 400,
      "output_width": 1,
      "output_height": 1,
      "quality": 82,
      "resized": false,
      "orientation": 1,
      "orientation_applied": false,
      "saved_bytes": 1,
      "saved_percent": 1
    }
  ],
  "summary": {}
}`
	if err := os.WriteFile(manifest, []byte(manifestContent), 0o644); err != nil {
		t.Fatal(err)
	}
	stubWebPDimensions(t, func(path string) (int, int, error) {
		return 1, 1, nil
	})

	var stdout bytes.Buffer
	summary, err := RunVerify(context.Background(), VerifyConfig{
		ManifestPath: manifest,
		MaxWidth:     1200,
	}, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Verified != 1 {
		t.Fatalf("expected one verified entry, got %#v", summary)
	}
}

func TestVerifyRejectsEqualSizedOutput(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sample.jpg")
	outputDir := filepath.Join(root, "generated")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, source, 800, 400)

	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(outputDir, "sample.webp")
	if err := os.WriteFile(output, bytes.Repeat([]byte("w"), int(sourceInfo.Size())), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := filepath.Join(root, "manifest.json")
	report := filepath.Join(root, "verify.jsonl")
	manifestContent := `{
  "version": 1,
  "generated_at": "2026-04-10T00:00:00Z",
  "command": "bulk",
  "root_dir": ".",
  "output_dir": "generated",
  "entries": [
    {
      "relative_path": "sample.jpg",
      "source_path": "sample.jpg",
      "output_path": "sample.webp",
      "source_size_bytes": ` + strconv.FormatInt(sourceInfo.Size(), 10) + `,
      "output_size_bytes": ` + strconv.FormatInt(sourceInfo.Size(), 10) + `,
      "width": 800,
      "height": 400,
      "output_width": 1,
      "output_height": 1,
      "quality": 82,
      "resized": false,
      "orientation": 1,
      "orientation_applied": false,
      "saved_bytes": 0,
      "saved_percent": 0
    }
  ],
  "summary": {}
}`
	if err := os.WriteFile(manifest, []byte(manifestContent), 0o644); err != nil {
		t.Fatal(err)
	}
	stubWebPDimensions(t, func(path string) (int, int, error) {
		return 1, 1, nil
	})

	var stdout bytes.Buffer
	summary, err := RunVerify(context.Background(), VerifyConfig{
		ManifestPath: manifest,
		ReportPath:   report,
		MaxWidth:     1200,
	}, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Failed != 1 || summary.Verified != 0 {
		t.Fatalf("expected equal-sized output to fail verification, got %#v", summary)
	}

	reportContent, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reportContent), `"status":"verify_size_regression"`) {
		t.Fatalf("expected verify_size_regression in report, got %s", reportContent)
	}
	var record FileRecord
	lines := strings.Split(strings.TrimSpace(string(reportContent)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected a single report record, got %d lines", len(lines))
	}
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(record.OutputPath) != filepath.Clean(output) {
		t.Fatalf("expected report output_path to resolve to %q, got %q", output, record.OutputPath)
	}
}

func TestRunProcessCommandReturnsReportCloseError(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sample.jpg")
	writeJPEG(t, source, 800, 400)

	stubReportWriterFactory(t, func(path string) (reportWriter, error) {
		return &stubReportWriter{closeErr: errors.New("flush failed")}, nil
	})

	cfg := testConfig(root)

	var stdout bytes.Buffer
	_, err := RunProcessCommand(context.Background(), cfg, newDimensionAwareFakeEncoder(t, 64, nil), &stdout)
	if err == nil {
		t.Fatal("expected report close error")
	}
	if !strings.Contains(err.Error(), "flush failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunVerifyReturnsReportCloseError(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "manifest.json")
	manifestContent := `{
  "version": 1,
  "generated_at": "2026-04-10T00:00:00Z",
  "command": "bulk",
  "root_dir": ".",
  "entries": [],
  "summary": {}
}`
	if err := os.WriteFile(manifest, []byte(manifestContent), 0o644); err != nil {
		t.Fatal(err)
	}

	stubReportWriterFactory(t, func(path string) (reportWriter, error) {
		return &stubReportWriter{closeErr: errors.New("flush failed")}, nil
	})

	var stdout bytes.Buffer
	_, err := RunVerify(context.Background(), VerifyConfig{
		ManifestPath: manifest,
		MaxWidth:     1200,
	}, &stdout)
	if err == nil {
		t.Fatal("expected report close error")
	}
	if !strings.Contains(err.Error(), "flush failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyReadsManifestWithAspectVariants(t *testing.T) {
	root := t.TempDir()
	artifactDir := t.TempDir()
	source := filepath.Join(root, "hero.jpg")
	writeJPEG(t, source, 2400, 1200)

	aspectVariants, err := parseAspectVariants("16:9,4:3,1:1")
	if err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(root)
	cfg.OutDir = filepath.Join(artifactDir, "out")
	cfg.ManifestPath = filepath.Join(artifactDir, "manifest.json")
	cfg.AspectVariants = aspectVariants

	var stdout bytes.Buffer
	summary, err := RunProcessCommand(context.Background(), cfg, newDimensionAwareFakeEncoder(t, 64, nil), &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Converted != 1 {
		t.Fatalf("expected one converted entry, got %#v", summary)
	}

	manifest, err := readConversionManifest(cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot, err := resolveConversionManifestSourceRoot(manifest, "")
	if err != nil {
		t.Fatal(err)
	}
	outputRoot, err := resolveConversionManifestOutputRoot(manifest)
	if err != nil {
		t.Fatal(err)
	}
	dimensionsByPath := map[string]image.Point{}
	for _, entry := range manifest.Entries {
		sourcePath, err := resolveArtifactPath(sourceRoot, entry.SourcePath)
		if err != nil {
			t.Fatal(err)
		}
		if sourcePath == "" {
			t.Fatal("expected resolved source path")
		}
		for _, variant := range manifestEntryOutputVariants(entry) {
			outputPath, err := resolveArtifactPath(outputRoot, variant.OutputPath)
			if err != nil {
				t.Fatal(err)
			}
			dimensionsByPath[outputPath] = image.Point{X: variant.OutputWidth, Y: variant.OutputHeight}
		}
	}
	stubWebPDimensions(t, func(path string) (int, int, error) {
		dimensions, ok := dimensionsByPath[path]
		if !ok {
			return 0, 0, fmt.Errorf("unexpected webp path %s", path)
		}
		return dimensions.X, dimensions.Y, nil
	})

	stdout.Reset()
	verifySummary, err := RunVerify(context.Background(), VerifyConfig{
		ManifestPath: cfg.ManifestPath,
		MaxWidth:     1200,
	}, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if verifySummary.Verified != 1 {
		t.Fatalf("expected one verified entry, got %#v", verifySummary)
	}
}

func TestLoadResumeSetRequiresMatchingFingerprintJSONL(t *testing.T) {
	root := t.TempDir()
	report := filepath.Join(root, "resume.jsonl")
	content := strings.Join([]string{
		`{"status":"converted","relative_path":"match.jpg","config_fingerprint":"keep"}`,
		`{"status":"converted","relative_path":"stale.jpg","config_fingerprint":"old"}`,
		`{"status":"converted","relative_path":"legacy.jpg"}`,
		`{"status":"failed_decode","relative_path":"failed.jpg","config_fingerprint":"keep"}`,
	}, "\n")
	if err := os.WriteFile(report, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	set, err := loadResumeSet(report, "keep")
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := set["match.jpg"]; !ok {
		t.Fatalf("expected matching fingerprint to be resumable: %#v", set)
	}
	if _, ok := set["stale.jpg"]; ok {
		t.Fatalf("did not expect stale fingerprint to resume: %#v", set)
	}
	if _, ok := set["legacy.jpg"]; ok {
		t.Fatalf("did not expect legacy report without fingerprint to resume: %#v", set)
	}
	if _, ok := set["failed.jpg"]; ok {
		t.Fatalf("did not expect failed status to be terminal: %#v", set)
	}
}

func TestLoadResumeSetAcceptsLargeJSONLRecords(t *testing.T) {
	root := t.TempDir()
	report := filepath.Join(root, "resume.jsonl")
	largeError := strings.Repeat("x", 80*1024)
	content := `{"status":"converted","relative_path":"large.jpg","config_fingerprint":"keep","error":"` + largeError + `"}` + "\n"
	if err := os.WriteFile(report, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	set, err := loadResumeSet(report, "keep")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set["large.jpg"]; !ok {
		t.Fatalf("expected large record to be resumable: %#v", set)
	}
}

func TestLoadResumeSetIgnoresLegacyCSVWithoutFingerprint(t *testing.T) {
	root := t.TempDir()
	report := filepath.Join(root, "resume.csv")
	content := "status,relative_path\nconverted,legacy.jpg\n"
	if err := os.WriteFile(report, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	set, err := loadResumeSet(report, "keep")
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 0 {
		t.Fatalf("expected legacy csv without fingerprint to be ignored, got %#v", set)
	}
}

func TestResumeSkipsWhenFingerprintMatches(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hero.jpg")
	writeJPEG(t, source, 1200, 600)

	firstReport := filepath.Join(root, "bulk.jsonl")
	cfg := testConfig(root)
	cfg.ExistingPolicy = existingOverwrite
	cfg.ReportPath = firstReport
	cfg = withConfigFingerprint(cfg)

	var stdout bytes.Buffer
	summary, err := RunProcessCommand(context.Background(), cfg, newDimensionAwareFakeEncoder(t, 64, nil), &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Converted != 1 {
		t.Fatalf("expected initial conversion, got %#v", summary)
	}

	resumeReport := filepath.Join(root, "resume.jsonl")
	resumeCfg := cfg
	resumeCfg.Mode = modeResume
	resumeCfg.ResumeFrom = firstReport
	resumeCfg.ReportPath = resumeReport
	resumeCfg = withConfigFingerprint(resumeCfg)

	stdout.Reset()
	summary, err = RunProcessCommand(context.Background(), resumeCfg, newDimensionAwareFakeEncoder(t, 64, nil), &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Skipped != 1 || summary.Converted != 0 {
		t.Fatalf("expected matching fingerprint to skip resume work, got %#v", summary)
	}

	reportContent, err := os.ReadFile(resumeReport)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reportContent), `"status":"skipped_resume"`) {
		t.Fatalf("expected skipped_resume record, got %s", reportContent)
	}
}

func TestResumeReprocessesWhenQualityChanges(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hero.jpg")
	writeJPEG(t, source, 1200, 600)

	firstReport := filepath.Join(root, "bulk.jsonl")
	cfg := testConfig(root)
	cfg.ExistingPolicy = existingOverwrite
	cfg.ReportPath = firstReport
	cfg = withConfigFingerprint(cfg)

	var stdout bytes.Buffer
	summary, err := RunProcessCommand(context.Background(), cfg, newDimensionAwareFakeEncoder(t, 64, nil), &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Converted != 1 {
		t.Fatalf("expected initial conversion, got %#v", summary)
	}

	resumeReport := filepath.Join(root, "resume.jsonl")
	resumeCfg := cfg
	resumeCfg.Mode = modeResume
	resumeCfg.ResumeFrom = firstReport
	resumeCfg.ReportPath = resumeReport
	resumeCfg.Quality = 90
	resumeCfg = withConfigFingerprint(resumeCfg)

	stdout.Reset()
	summary, err = RunProcessCommand(context.Background(), resumeCfg, newDimensionAwareFakeEncoder(t, 64, nil), &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Converted != 1 || summary.Skipped != 0 {
		t.Fatalf("expected quality change to force reprocessing, got %#v", summary)
	}

	reportContent, err := os.ReadFile(resumeReport)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reportContent), `"status":"converted"`) {
		t.Fatalf("expected converted record after config change, got %s", reportContent)
	}
}

func TestWalkTreeRejectsUnsupportedImageFormats(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "anim.gif"), []byte("gif"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "already.webp"), []byte("webp"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(root)
	jobs := make(chan FileJob, 1)
	results := make(chan FileRecord, 8)
	progress := newProgressReporterWithWriter(io.Discard, false, "test", 0)

	if err := walkTree(context.Background(), cfg, jobs, results, map[string]struct{}{}, progress); err != nil {
		t.Fatal(err)
	}
	close(jobs)
	close(results)

	got := map[string]FileRecord{}
	for record := range results {
		got[record.RelativePath] = record
	}

	if got["anim.gif"].Status != "rejected_unsupported_format" {
		t.Fatalf("expected anim.gif to be rejected, got %#v", got["anim.gif"])
	}
	if got["already.webp"].Status != "rejected_unsupported_format" {
		t.Fatalf("expected already.webp to be rejected, got %#v", got["already.webp"])
	}
}

func TestWalkTreeDedupesDirectoriesReachedViaSymlink(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "images")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, filepath.Join(realDir, "hero.jpg"), 1600, 800)

	aliasDir := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, aliasDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	cfg := testConfig(root)
	cfg.FollowSymlinks = true

	jobs := make(chan FileJob, 8)
	results := make(chan FileRecord, 8)
	progress := newProgressReporterWithWriter(io.Discard, false, "test", 0)

	if err := walkTree(context.Background(), cfg, jobs, results, map[string]struct{}{}, progress); err != nil {
		t.Fatal(err)
	}
	close(jobs)
	close(results)

	var got []FileJob
	for job := range jobs {
		got = append(got, job)
	}

	if len(got) != 1 {
		t.Fatalf("expected exactly one job after deduping symlinked directory, got %#v", got)
	}
	if got[0].RelativePath != "alias/hero.jpg" && got[0].RelativePath != "images/hero.jpg" {
		t.Fatalf("unexpected walked path %#v", got[0])
	}
}

func TestWalkTreeSkipsOutDirReachedViaSymlinkAlias(t *testing.T) {
	root := t.TempDir()
	imagesDir := filepath.Join(root, "images")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, filepath.Join(imagesDir, "hero.jpg"), 1600, 800)

	outDir := filepath.Join(root, "generated")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "hero.webp"), []byte("webp"), 0o644); err != nil {
		t.Fatal(err)
	}

	aliasDir := filepath.Join(root, "generated-alias")
	if err := os.Symlink(outDir, aliasDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	cfg := testConfig(root)
	cfg.OutDir = outDir
	cfg.FollowSymlinks = true

	jobs := make(chan FileJob, 8)
	results := make(chan FileRecord, 8)
	progress := newProgressReporterWithWriter(io.Discard, false, "test", 0)

	if err := walkTree(context.Background(), cfg, jobs, results, map[string]struct{}{}, progress); err != nil {
		t.Fatal(err)
	}
	close(jobs)
	close(results)

	var gotJobs []FileJob
	for job := range jobs {
		gotJobs = append(gotJobs, job)
	}
	if len(gotJobs) != 1 || gotJobs[0].RelativePath != "images/hero.jpg" {
		t.Fatalf("expected only the source image to be queued, got %#v", gotJobs)
	}

	for record := range results {
		if strings.Contains(record.RelativePath, "generated") || strings.Contains(record.RelativePath, ".webp") {
			t.Fatalf("expected out-dir aliases to be skipped, got %#v", record)
		}
	}
}

func TestPathWithinRootRejectsParentDirectory(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Dir(root)

	inside, err := pathWithinRoot(root, parent)
	if err != nil {
		t.Fatal(err)
	}
	if inside {
		t.Fatalf("expected parent directory %q to be outside root %q", parent, root)
	}
}

func TestProcessConfigCPUOptionCapsAutoWorkers(t *testing.T) {
	raw := newProcessFlagValues()
	raw.rootDir = t.TempDir()
	raw.cpus = "1"
	raw.workers = "auto"

	cfg, err := raw.toProcessConfig(modeBulk)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CPUs != 1 {
		t.Fatalf("expected CPUs=1, got %d", cfg.CPUs)
	}
	if cfg.Workers != 1 {
		t.Fatalf("expected auto workers to follow CPU limit, got %d", cfg.Workers)
	}
}

func TestProcessConfigRejectsWorkersAboveCPULimit(t *testing.T) {
	raw := newProcessFlagValues()
	raw.rootDir = t.TempDir()
	raw.cpus = "1"
	raw.workers = "2"

	_, err := raw.toProcessConfig(modeBulk)
	if err == nil {
		t.Fatal("expected workers above CPU limit to fail")
	}
	if !strings.Contains(err.Error(), "cannot exceed cpus") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testConfig(root string) ProcessConfig {
	return ProcessConfig{
		Mode:             modeBulk,
		RootDir:          root,
		Extensions:       map[string]struct{}{"jpg": {}, "jpeg": {}, "png": {}},
		ExtensionList:    []string{"jpg", "jpeg", "png"},
		MaxFileSizeBytes: 100 * 1024 * 1024,
		MaxPixels:        80_000_000,
		MaxDimension:     20_000,
		MaxWidth:         1200,
		CropMode:         cropModeSafe,
		FocusX:           0.5,
		FocusY:           0.5,
		Quality:          82,
		Workers:          1,
		WorkersRaw:       "1",
		ExistingPolicy:   existingOverwrite,
	}
}

func withConfigFingerprint(cfg ProcessConfig) ProcessConfig {
	cfg.ConfigFingerprint = processConfigFingerprint(cfg)
	return cfg
}

func writePNG(t *testing.T, path string, width int, height int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fillGradient(img)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeQuietly(file)
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func writeJPEG(t *testing.T, path string, width int, height int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fillGradient(img)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeQuietly(file)
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
}

func fillGradient(img *image.NRGBA) {
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x * 255) / max(1, img.Bounds().Dx()-1)),
				G: uint8((y * 255) / max(1, img.Bounds().Dy()-1)),
				B: 180,
				A: 255,
			})
		}
	}
}

func writeJPEGWithOrientation(t *testing.T, path string, width int, height int, orientation uint16) {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	fillGradient(img)

	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}

	exif := minimalExifOrientation(orientation)
	data := jpegBuf.Bytes()
	out := append([]byte{}, data[:2]...)
	out = append(out, exif...)
	out = append(out, data[2:]...)

	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func minimalExifOrientation(orientation uint16) []byte {
	tiff := new(bytes.Buffer)
	tiff.WriteString("MM")
	_ = binary.Write(tiff, binary.BigEndian, uint16(42))
	_ = binary.Write(tiff, binary.BigEndian, uint32(8))
	_ = binary.Write(tiff, binary.BigEndian, uint16(1))
	_ = binary.Write(tiff, binary.BigEndian, uint16(0x0112))
	_ = binary.Write(tiff, binary.BigEndian, uint16(3))
	_ = binary.Write(tiff, binary.BigEndian, uint32(1))
	_ = binary.Write(tiff, binary.BigEndian, orientation)
	_ = binary.Write(tiff, binary.BigEndian, uint16(0))
	_ = binary.Write(tiff, binary.BigEndian, uint32(0))

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	segment := new(bytes.Buffer)
	segment.Write([]byte{0xFF, 0xE1})
	_ = binary.Write(segment, binary.BigEndian, uint16(len(payload)+2))
	segment.Write(payload)
	return segment.Bytes()
}
