package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
)

type Encoder interface {
	Check() error
	Encode(ctx context.Context, inputPath string, outputPath string, quality int) error
}

type CWebPEncoder struct {
	Binary string
}

func newCWebPEncoder(binary string) *CWebPEncoder {
	return &CWebPEncoder{Binary: binary}
}

func (e *CWebPEncoder) Check() error {
	if _, err := exec.LookPath(e.Binary); err != nil {
		return fmt.Errorf("cwebp is required for bulk/resize work: %w", err)
	}
	return nil
}

func (e *CWebPEncoder) Encode(ctx context.Context, inputPath string, outputPath string, quality int) error {
	args := []string{
		"-quiet",
		"-q", strconv.Itoa(quality),
		"-metadata", "none",
		inputPath,
		"-o", outputPath,
	}
	cmd := exec.CommandContext(ctx, e.Binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, message)
	}
	return nil
}

func processJob(ctx context.Context, cfg ProcessConfig, job FileJob, encoder Encoder) FileRecord {
	start := time.Now()
	record := newRecord(cfg.Mode, cfg.ConfigFingerprint, job.Path, job.RelativePath, job.Extension, "")
	record.Quality = cfg.Quality
	record.MaxWidth = cfg.MaxWidth
	variantPlans := buildOutputVariantPlans(cfg, job)
	if len(variantPlans) > 0 {
		record.OutputPath = variantPlans[0].OutputPath
	}

	if err := ctx.Err(); err != nil {
		return canceledRecord(record, start, err)
	}

	if cfg.Mode != modeScan {
		existingReason := "one or more outputs already exist"
		if len(variantPlans) == 1 {
			existingReason = "output already exists"
		}
		switch cfg.ExistingPolicy {
		case existingFail:
			if outputPath, ok := firstExistingOutputPath(variantPlans); ok {
				record.Status = "failed_existing_output"
				record.OutputPath = outputPath
				record.Reason = existingReason
				record.DurationMillis = time.Since(start).Milliseconds()
				return record
			}
		case existingSkip:
			if outputPath, ok := firstExistingOutputPath(variantPlans); ok {
				record.Status = "skipped_existing"
				record.OutputPath = outputPath
				record.Reason = existingReason
				record.DurationMillis = time.Since(start).Milliseconds()
				return record
			}
		}
	}

	info, err := os.Stat(job.Path)
	if err != nil {
		record.Status = "failed_stat"
		record.Error = err.Error()
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}
	record.FileSizeBytes = info.Size()

	if record.FileSizeBytes > cfg.MaxFileSizeBytes {
		record.Status = "rejected_file_too_large"
		record.Reason = fmt.Sprintf("file size %d exceeds limit %d", record.FileSizeBytes, cfg.MaxFileSizeBytes)
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}

	magic, err := detectMagic(job.Path)
	if err != nil {
		record.Status = "failed_read"
		record.Error = err.Error()
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}
	record.Magic = magic

	expectedMagic := knownImageExtensions[job.Extension]
	if magic == "unknown" {
		record.Status = "rejected_unknown_magic"
		record.Reason = "unable to identify image magic bytes"
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}
	if expectedMagic != magic {
		record.Status = "rejected_magic_mismatch"
		record.Reason = fmt.Sprintf("extension %s does not match magic %s", job.Extension, magic)
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}

	width, height, err := decodeDimensions(job.Path, magic)
	if err != nil {
		record.Status = "failed_decode_config"
		record.Error = err.Error()
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}
	record.Width = width
	record.Height = height
	record.Pixels = int64(width) * int64(height)

	if width <= 0 || height <= 0 {
		record.Status = "rejected_dimensions"
		record.Reason = "image dimensions must be positive"
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}
	if width > cfg.MaxDimension || height > cfg.MaxDimension {
		record.Status = "rejected_dimensions"
		record.Reason = fmt.Sprintf("dimensions %dx%d exceed limit %d", width, height, cfg.MaxDimension)
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}
	if record.Pixels > cfg.MaxPixels {
		record.Status = "rejected_pixels"
		record.Reason = fmt.Sprintf("pixel count %d exceeds limit %d", record.Pixels, cfg.MaxPixels)
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}

	orientation := 1
	if magic == "jpeg" {
		orientation, _ = readJPEGOrientation(job.Path)
	}
	record.Orientation = orientation

	if err := ctx.Err(); err != nil {
		return canceledRecord(record, start, err)
	}

	img, err := decodeImage(job.Path, magic)
	if err != nil {
		record.Status = "failed_decode"
		record.Error = err.Error()
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}

	if orientation != 1 {
		img = applyOrientation(img, orientation)
		record.OrientationApplied = true
	}

	if cfg.Mode == modeScan {
		record.Status = "scanned"
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}

	variantInfos := make([]OutputVariantInfo, 0, len(variantPlans))
	renderedVariants := make([]preparedVariantOutput, 0, len(variantPlans))
	defer func() {
		for _, variant := range renderedVariants {
			if variant.cleanup != nil {
				variant.cleanup()
			}
		}
	}()

	discardedSupporting := 0
	for index, plan := range variantPlans {
		if err := ctx.Err(); err != nil {
			return canceledRecord(record, start, err)
		}

		variantImage, cropped, resized := renderOutputVariant(img, cfg, plan)
		variantInfo := OutputVariantInfo{
			Name:         plan.Name,
			Usage:        plan.Usage,
			AspectRatio:  plan.AspectRatio,
			OutputPath:   plan.OutputPath,
			OutputWidth:  variantImage.Bounds().Dx(),
			OutputHeight: variantImage.Bounds().Dy(),
			Resized:      resized,
			Cropped:      cropped,
		}

		if cfg.DryRun {
			variantInfos = append(variantInfos, variantInfo)
			continue
		}

		// Always normalize pixels through a metadata-free temp PNG before cwebp so
		// the output never inherits source-container metadata or ancillary chunks.
		inputPath, cleanupInput, err := prepareEncoderInput(variantImage, filepath.Dir(plan.OutputPath))
		if err != nil {
			record.Status = "failed_write_temp"
			record.Error = err.Error()
			record.DurationMillis = time.Since(start).Milliseconds()
			return record
		}

		tempOutput, cleanupOutput, err := prepareTempOutput(plan.OutputPath)
		if err != nil {
			cleanupInput()
			record.Status = "failed_write_temp"
			record.Error = err.Error()
			record.DurationMillis = time.Since(start).Milliseconds()
			return record
		}

		if err := encoder.Encode(ctx, inputPath, tempOutput, cfg.Quality); err != nil {
			cleanupInput()
			cleanupOutput()
			record.Status = "failed_encode"
			record.Error = decorateVariantError(plan, err.Error())
			record.DurationMillis = time.Since(start).Milliseconds()
			return record
		}
		cleanupInput()

		outputInfo, err := os.Stat(tempOutput)
		if err != nil {
			cleanupOutput()
			record.Status = "failed_stat_output"
			record.Error = decorateVariantError(plan, err.Error())
			record.DurationMillis = time.Since(start).Milliseconds()
			return record
		}

		actualWidth, actualHeight, err := webpDimensionsDecoder(tempOutput)
		if err != nil {
			cleanupOutput()
			record.Status = "failed_decode_output"
			record.Error = decorateVariantError(plan, err.Error())
			record.DurationMillis = time.Since(start).Milliseconds()
			return record
		}
		if actualWidth != variantInfo.OutputWidth || actualHeight != variantInfo.OutputHeight {
			cleanupOutput()
			record.Status = "failed_output_mismatch"
			record.Reason = decorateVariantError(plan, fmt.Sprintf(
				"expected output %dx%d but encoder wrote %dx%d",
				variantInfo.OutputWidth,
				variantInfo.OutputHeight,
				actualWidth,
				actualHeight,
			))
			record.DurationMillis = time.Since(start).Milliseconds()
			return record
		}
		if actualWidth > cfg.MaxWidth {
			cleanupOutput()
			record.Status = "failed_output_mismatch"
			record.Reason = decorateVariantError(plan, fmt.Sprintf("encoder wrote width %d above limit %d", actualWidth, cfg.MaxWidth))
			record.DurationMillis = time.Since(start).Milliseconds()
			return record
		}

		variantInfo.OutputSizeBytes = outputInfo.Size()
		variantInfo.SavedBytes = record.FileSizeBytes - variantInfo.OutputSizeBytes
		if variantInfo.OutputSizeBytes >= record.FileSizeBytes {
			cleanupOutput()
			if index == 0 {
				record.Status = "discarded_larger"
				record.Reason = "generated webp is larger than the source"
				record.DurationMillis = time.Since(start).Milliseconds()
				return record
			}
			discardedSupporting++
			continue
		}

		variantInfo.SavedPercent = (float64(variantInfo.SavedBytes) / float64(record.FileSizeBytes)) * 100
		variantInfos = append(variantInfos, variantInfo)
		renderedVariants = append(renderedVariants, preparedVariantOutput{
			outputPath: plan.OutputPath,
			tempPath:   tempOutput,
			cleanup:    cleanupOutput,
		})
	}

	if len(variantInfos) > 0 {
		record.OutputVariants = variantInfos
		if primaryVariant, ok := primaryOutputVariant(variantInfos); ok {
			applyPrimaryVariant(&record, primaryVariant)
		}
	}

	if cfg.DryRun {
		record.Status = "dry_run"
		record.DryRun = true
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}

	if len(record.OutputVariants) == 0 {
		record.Status = "discarded_larger"
		record.Reason = "generated webp is larger than the source"
		record.DurationMillis = time.Since(start).Milliseconds()
		return record
	}

	for index := range renderedVariants {
		if err := ctx.Err(); err != nil {
			return canceledRecord(record, start, err)
		}

		if err := commitTempOutput(renderedVariants[index].tempPath, renderedVariants[index].outputPath); err != nil {
			record.Status = "failed_commit"
			record.Error = decorateVariantPathError(renderedVariants[index].outputPath, err.Error())
			record.DurationMillis = time.Since(start).Milliseconds()
			return record
		}
		renderedVariants[index].cleanup = nil
	}

	if discardedSupporting > 0 {
		record.Status = "converted_partial"
		record.Reason = fmt.Sprintf("discarded %d supporting variant(s) larger than the source", discardedSupporting)
	} else {
		record.Status = "converted"
	}
	record.DurationMillis = time.Since(start).Milliseconds()
	return record
}

func canceledRecord(record FileRecord, start time.Time, err error) FileRecord {
	record.Status = "failed_canceled"
	record.Error = err.Error()
	record.DurationMillis = time.Since(start).Milliseconds()
	return record
}

type preparedVariantOutput struct {
	outputPath string
	tempPath   string
	cleanup    func()
}

func firstExistingOutputPath(plans []outputVariantPlan) (string, bool) {
	for _, plan := range plans {
		if _, err := os.Stat(plan.OutputPath); err == nil {
			return plan.OutputPath, true
		}
	}
	return "", false
}

func renderOutputVariant(img image.Image, cfg ProcessConfig, plan outputVariantPlan) (image.Image, bool, bool) {
	rendered := img
	cropped := false
	if plan.Numerator > 0 && plan.Denominator > 0 {
		rendered, cropped = cropImageToAspect(rendered, plan.Numerator, plan.Denominator, cfg.CropMode, cfg.FocusX, cfg.FocusY)
	}

	resized := false
	if rendered.Bounds().Dx() > cfg.MaxWidth {
		rendered = resizeImage(rendered, cfg.MaxWidth)
		resized = true
	}
	return rendered, cropped, resized
}

func cropImageToAspect(img image.Image, numerator int, denominator int, mode CropMode, focusX float64, focusY float64) (image.Image, bool) {
	srcBounds := img.Bounds()
	srcWidth := srcBounds.Dx()
	srcHeight := srcBounds.Dy()
	if srcWidth <= 0 || srcHeight <= 0 || numerator <= 0 || denominator <= 0 {
		return img, false
	}

	targetRatio := float64(numerator) / float64(denominator)
	srcRatio := float64(srcWidth) / float64(srcHeight)

	cropWidth := srcWidth
	cropHeight := srcHeight
	if srcRatio > targetRatio {
		cropWidth = int(float64(srcHeight) * targetRatio)
	} else if srcRatio < targetRatio {
		cropHeight = int(float64(srcWidth) / targetRatio)
	}

	if cropWidth < 1 {
		cropWidth = 1
	}
	if cropHeight < 1 {
		cropHeight = 1
	}
	if cropWidth > srcWidth {
		cropWidth = srcWidth
	}
	if cropHeight > srcHeight {
		cropHeight = srcHeight
	}
	if cropWidth == srcWidth && cropHeight == srcHeight {
		return img, false
	}

	if mode != cropModeFocus {
		focusX = 0.5
		focusY = 0.5
	}

	originX := focusedCropOrigin(srcWidth, cropWidth, focusX)
	originY := focusedCropOrigin(srcHeight, cropHeight, focusY)
	cropRect := image.Rect(
		srcBounds.Min.X+originX,
		srcBounds.Min.Y+originY,
		srcBounds.Min.X+originX+cropWidth,
		srcBounds.Min.Y+originY+cropHeight,
	)

	dst := image.NewNRGBA(image.Rect(0, 0, cropWidth, cropHeight))
	stddraw.Draw(dst, dst.Bounds(), img, cropRect.Min, stddraw.Src)
	return dst, true
}

func focusedCropOrigin(sourceSpan int, cropSpan int, focus float64) int {
	if cropSpan >= sourceSpan {
		return 0
	}
	if focus < 0 {
		focus = 0
	}
	if focus > 1 {
		focus = 1
	}

	focusPoint := int(focus * float64(sourceSpan))
	origin := focusPoint - cropSpan/2
	if origin < 0 {
		return 0
	}
	maxOrigin := sourceSpan - cropSpan
	if origin > maxOrigin {
		return maxOrigin
	}
	return origin
}

func applyPrimaryVariant(record *FileRecord, variant OutputVariantInfo) {
	record.OutputPath = variant.OutputPath
	record.OutputWidth = variant.OutputWidth
	record.OutputHeight = variant.OutputHeight
	record.OutputSizeBytes = variant.OutputSizeBytes
	record.SavedBytes = variant.SavedBytes
	record.SavedPercent = variant.SavedPercent
	record.Resized = variant.Resized
}

func decorateVariantError(plan outputVariantPlan, message string) string {
	label := plan.AspectRatio
	if strings.TrimSpace(label) == "" {
		label = "primary"
	}
	return fmt.Sprintf("%s variant: %s", label, message)
}

func decorateVariantPathError(path string, message string) string {
	return fmt.Sprintf("%s: %s", filepath.Base(path), message)
}

func detectMagic(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()

	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	buf = buf[:n]

	switch {
	case len(buf) >= 3 && bytes.Equal(buf[:3], []byte{0xFF, 0xD8, 0xFF}):
		return "jpeg", nil
	case len(buf) >= 8 && bytes.Equal(buf[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return "png", nil
	case len(buf) >= 12 && bytes.Equal(buf[:4], []byte("RIFF")) && bytes.Equal(buf[8:12], []byte("WEBP")):
		return "webp", nil
	case len(buf) >= 6 && (bytes.Equal(buf[:6], []byte("GIF87a")) || bytes.Equal(buf[:6], []byte("GIF89a"))):
		return "gif", nil
	case len(buf) >= 12 && bytes.Equal(buf[4:8], []byte("ftyp")):
		brand := string(buf[8:12])
		switch brand {
		case "avif", "avis":
			return "avif", nil
		case "heic", "heix", "hevc", "hevx", "mif1", "msf1":
			return "heic", nil
		}
	case looksLikeSVG(buf):
		return "svg", nil
	}

	return "unknown", nil
}

func looksLikeSVG(buf []byte) bool {
	trimmed := bytes.TrimSpace(buf)
	if bytes.HasPrefix(trimmed, []byte("<svg")) {
		return true
	}
	if bytes.HasPrefix(trimmed, []byte("<?xml")) && bytes.Contains(trimmed, []byte("<svg")) {
		return true
	}
	return false
}

func decodeDimensions(path string, magic string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		_ = file.Close()
	}()

	switch magic {
	case "jpeg":
		cfg, err := jpeg.DecodeConfig(file)
		if err != nil {
			return 0, 0, err
		}
		return cfg.Width, cfg.Height, nil
	case "png":
		cfg, err := png.DecodeConfig(file)
		if err != nil {
			return 0, 0, err
		}
		return cfg.Width, cfg.Height, nil
	default:
		return 0, 0, fmt.Errorf("unsupported decode config for %s", magic)
	}
}

func decodeImage(path string, magic string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer closeQuietly(file)

	switch magic {
	case "jpeg":
		return jpeg.Decode(file)
	case "png":
		return png.Decode(file)
	default:
		return nil, fmt.Errorf("unsupported decode for %s", magic)
	}
}

func resizeImage(img image.Image, maxWidth int) image.Image {
	srcBounds := img.Bounds()
	srcWidth := srcBounds.Dx()
	srcHeight := srcBounds.Dy()
	if srcWidth <= maxWidth {
		return img
	}

	scale := float64(maxWidth) / float64(srcWidth)
	dstHeight := int(float64(srcHeight) * scale)
	if dstHeight < 1 {
		dstHeight = 1
	}

	dst := image.NewNRGBA(image.Rect(0, 0, maxWidth, dstHeight))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, srcBounds, xdraw.Over, nil)
	return dst
}

func applyOrientation(img image.Image, orientation int) image.Image {
	switch orientation {
	case 2:
		return remapImage(img, img.Bounds().Dx(), img.Bounds().Dy(), func(x, y, w, h int) (int, int) {
			return w - 1 - x, y
		})
	case 3:
		return remapImage(img, img.Bounds().Dx(), img.Bounds().Dy(), func(x, y, w, h int) (int, int) {
			return w - 1 - x, h - 1 - y
		})
	case 4:
		return remapImage(img, img.Bounds().Dx(), img.Bounds().Dy(), func(x, y, w, h int) (int, int) {
			return x, h - 1 - y
		})
	case 5:
		return remapImage(img, img.Bounds().Dy(), img.Bounds().Dx(), func(x, y, w, h int) (int, int) {
			return y, x
		})
	case 6:
		return remapImage(img, img.Bounds().Dy(), img.Bounds().Dx(), func(x, y, w, h int) (int, int) {
			return y, h - 1 - x
		})
	case 7:
		return remapImage(img, img.Bounds().Dy(), img.Bounds().Dx(), func(x, y, w, h int) (int, int) {
			return w - 1 - y, h - 1 - x
		})
	case 8:
		return remapImage(img, img.Bounds().Dy(), img.Bounds().Dx(), func(x, y, w, h int) (int, int) {
			return w - 1 - y, x
		})
	default:
		return img
	}
}

func remapImage(img image.Image, dstWidth int, dstHeight int, mapFn func(x, y, w, h int) (int, int)) image.Image {
	srcBounds := img.Bounds()
	w := srcBounds.Dx()
	h := srcBounds.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	for y := 0; y < dstHeight; y++ {
		for x := 0; x < dstWidth; x++ {
			srcX, srcY := mapFn(x, y, w, h)
			c := color.NRGBAModel.Convert(img.At(srcBounds.Min.X+srcX, srcBounds.Min.Y+srcY)).(color.NRGBA)
			dst.SetNRGBA(x, y, c)
		}
	}
	return dst
}

func prepareEncoderInput(img image.Image, dir string) (string, func(), error) {
	tempInput, err := writeTempPNG(img, dir)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.Remove(tempInput)
	}
	return tempInput, cleanup, nil
}

func writeTempPNG(img image.Image, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, ".webp-guard-input-*.png")
	if err != nil {
		return "", err
	}
	defer closeQuietly(file)

	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(file, img); err != nil {
		return "", err
	}
	return file.Name(), nil
}

func prepareTempOutput(finalPath string) (string, func(), error) {
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return "", nil, err
	}
	file, err := os.CreateTemp(filepath.Dir(finalPath), ".webp-guard-output-*.webp")
	if err != nil {
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.Remove(file.Name())
	}
	return file.Name(), cleanup, nil
}

func commitTempOutput(tempPath string, finalPath string) error {
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(finalPath); err != nil {
		if os.IsNotExist(err) {
			return os.Rename(tempPath, finalPath)
		}
		return err
	}

	backupPath := finalPath + ".webp-guard-backup"
	_ = os.Remove(backupPath)

	if err := os.Rename(finalPath, backupPath); err != nil {
		return err
	}

	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Rename(backupPath, finalPath)
		return err
	}

	return os.Remove(backupPath)
}

func buildOutputPath(cfg ProcessConfig, job FileJob) string {
	if cfg.OutDir == "" {
		return strings.TrimSuffix(job.Path, filepath.Ext(job.Path)) + ".webp"
	}
	relativePath := strings.TrimSuffix(filepath.FromSlash(job.RelativePath), filepath.Ext(job.RelativePath)) + ".webp"
	return filepath.Join(cfg.OutDir, relativePath)
}

func readJPEGOrientation(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 1, err
	}
	defer closeQuietly(file)

	var header [2]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return 1, err
	}
	if header != [2]byte{0xFF, 0xD8} {
		return 1, fmt.Errorf("not a jpeg")
	}

	for {
		var markerPrefix [1]byte
		if _, err := io.ReadFull(file, markerPrefix[:]); err != nil {
			if err == io.EOF {
				return 1, nil
			}
			return 1, err
		}
		for markerPrefix[0] != 0xFF {
			if _, err := io.ReadFull(file, markerPrefix[:]); err != nil {
				return 1, nil
			}
		}

		var marker [1]byte
		if _, err := io.ReadFull(file, marker[:]); err != nil {
			return 1, err
		}
		for marker[0] == 0xFF {
			if _, err := io.ReadFull(file, marker[:]); err != nil {
				return 1, err
			}
		}

		if marker[0] == 0xDA || marker[0] == 0xD9 {
			return 1, nil
		}

		var segmentLength [2]byte
		if _, err := io.ReadFull(file, segmentLength[:]); err != nil {
			return 1, err
		}
		size := int(binary.BigEndian.Uint16(segmentLength[:]))
		if size < 2 {
			return 1, fmt.Errorf("invalid jpeg segment length")
		}

		segment := make([]byte, size-2)
		if _, err := io.ReadFull(file, segment); err != nil {
			return 1, err
		}

		if marker[0] == 0xE1 && len(segment) >= 6 && bytes.Equal(segment[:6], []byte("Exif\x00\x00")) {
			orientation, err := parseTIFFOrientation(segment[6:])
			if err != nil {
				return 1, nil
			}
			return orientation, nil
		}
	}
}

func parseTIFFOrientation(tiff []byte) (int, error) {
	if len(tiff) < 8 {
		return 1, fmt.Errorf("short tiff header")
	}

	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1, fmt.Errorf("invalid byte order")
	}

	if order.Uint16(tiff[2:4]) != 42 {
		return 1, fmt.Errorf("invalid tiff marker")
	}

	ifdOffset := int(order.Uint32(tiff[4:8]))
	if ifdOffset < 0 || ifdOffset+2 > len(tiff) {
		return 1, fmt.Errorf("invalid ifd offset")
	}

	entryCount := int(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
	offset := ifdOffset + 2
	for i := 0; i < entryCount; i++ {
		entryStart := offset + (i * 12)
		entryEnd := entryStart + 12
		if entryEnd > len(tiff) {
			break
		}

		tag := order.Uint16(tiff[entryStart : entryStart+2])
		if tag != 0x0112 {
			continue
		}

		typ := order.Uint16(tiff[entryStart+2 : entryStart+4])
		count := order.Uint32(tiff[entryStart+4 : entryStart+8])
		if typ != 3 || count < 1 {
			return 1, fmt.Errorf("unsupported orientation field")
		}

		value := order.Uint16(tiff[entryStart+8 : entryStart+10])
		if value >= 1 && value <= 8 {
			return int(value), nil
		}
		return 1, nil
	}

	return 1, nil
}
