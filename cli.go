package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	exitOK          = 0
	exitConfigError = 1
	exitScanIssues  = 2
	exitVerifyIssue = 3
	exitInterrupted = 130
)

type CommandMode string

const (
	modeBulk   CommandMode = "bulk"
	modeScan   CommandMode = "scan"
	modeResume CommandMode = "resume"
	modeVerify CommandMode = "verify"
)

type ExistingPolicy string

const (
	existingSkip      ExistingPolicy = "skip"
	existingOverwrite ExistingPolicy = "overwrite"
	existingFail      ExistingPolicy = "fail"
)

type ProcessConfig struct {
	Mode              CommandMode
	RootDir           string
	OutDir            string
	ConfigFingerprint string
	Extensions        map[string]struct{}
	ExtensionList     []string
	IncludePatterns   []GlobPattern
	ExcludePatterns   []GlobPattern
	IncludeHidden     bool
	FollowSymlinks    bool
	MaxFileSizeBytes  int64
	MaxPixels         int64
	MaxDimension      int
	MaxWidth          int
	AspectVariants    []AspectVariantConfig
	CropMode          CropMode
	FocusX            float64
	FocusY            float64
	Quality           int
	DryRun            bool
	CPUs              int
	CPUsRaw           string
	Workers           int
	WorkersRaw        string
	ExistingPolicy    ExistingPolicy
	ReportPath        string
	ManifestPath      string
	ResumeFrom        string
}

type VerifyConfig struct {
	RootDir      string
	ManifestPath string
	ReportPath   string
	MaxWidth     int
	CPUs         int
	CPUsRaw      string
}

type stringListFlag []string

func parseCommandFlags(fs *flag.FlagSet, args []string) (bool, error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func (s *stringListFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringListFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type overridingStringListValue struct {
	target  *stringListFlag
	touched bool
}

func newOverridingStringListValue(target *stringListFlag) *overridingStringListValue {
	return &overridingStringListValue{target: target}
}

func (v *overridingStringListValue) String() string {
	if v == nil || v.target == nil {
		return ""
	}
	return v.target.String()
}

func (v *overridingStringListValue) Set(value string) error {
	if !v.touched {
		*v.target = (*v.target)[:0]
		v.touched = true
	}
	return v.target.Set(value)
}

func runBulkCommand(ctx context.Context, args []string, encoder Encoder, stdout, stderr io.Writer, legacy bool) (int, error) {
	runtimeCfg, err := loadRuntimeConfig(args)
	if err != nil {
		return exitConfigError, err
	}

	fs := flag.NewFlagSet("bulk", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := newProcessFlagValues()
	applyProcessFileConfig(raw, runtimeCfg.File.Process, runtimeCfg.BaseDir)
	applyProcessFileConfig(raw, runtimeCfg.File.Bulk, runtimeCfg.BaseDir)
	bindConfigFlags(fs, &runtimeCfg.Selection)
	bindProcessFlags(fs, raw, true)
	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "Emit the command summary as JSON on stdout")

	if legacy {
		fs.Usage = func() {
			printLegacyBulkUsage(stderr)
		}
	} else {
		fs.Usage = func() {
			printBulkUsage(stderr)
		}
	}

	helpShown, err := parseCommandFlags(fs, args)
	if err != nil {
		return exitConfigError, err
	}
	if helpShown {
		return exitOK, nil
	}

	cfg, err := raw.toProcessConfig(modeBulk)
	if err != nil {
		return exitConfigError, err
	}

	summary, exitCode, err := runProcess(ctx, cfg, encoder, stderr)
	if err != nil {
		return exitConfigError, err
	}
	if jsonOutput {
		if err := writeJSONValue(stdout, summary); err != nil {
			return exitConfigError, err
		}
	}
	return exitCode, nil
}

func runScanCommand(ctx context.Context, args []string, encoder Encoder, stdout, stderr io.Writer) (int, error) {
	runtimeCfg, err := loadRuntimeConfig(args)
	if err != nil {
		return exitConfigError, err
	}

	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := newProcessFlagValues()
	applyProcessFileConfig(raw, runtimeCfg.File.Process, runtimeCfg.BaseDir)
	applyProcessFileConfig(raw, runtimeCfg.File.Scan, runtimeCfg.BaseDir)
	bindConfigFlags(fs, &runtimeCfg.Selection)
	bindProcessFlags(fs, raw, false)
	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "Emit the command summary as JSON on stdout")

	fs.Usage = func() {
		printScanUsage(stderr)
	}

	helpShown, err := parseCommandFlags(fs, args)
	if err != nil {
		return exitConfigError, err
	}
	if helpShown {
		return exitOK, nil
	}

	cfg, err := raw.toProcessConfig(modeScan)
	if err != nil {
		return exitConfigError, err
	}

	summary, exitCode, err := runProcess(ctx, cfg, encoder, stderr)
	if err != nil {
		return exitConfigError, err
	}
	if jsonOutput {
		if err := writeJSONValue(stdout, summary); err != nil {
			return exitConfigError, err
		}
	}
	return exitCode, nil
}

func runResumeCommand(ctx context.Context, args []string, encoder Encoder, stdout, stderr io.Writer) (int, error) {
	runtimeCfg, err := loadRuntimeConfig(args)
	if err != nil {
		return exitConfigError, err
	}

	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := newProcessFlagValues()
	applyProcessFileConfig(raw, runtimeCfg.File.Process, runtimeCfg.BaseDir)
	applyProcessFileConfig(raw, runtimeCfg.File.Resume, runtimeCfg.BaseDir)
	bindConfigFlags(fs, &runtimeCfg.Selection)
	bindProcessFlags(fs, raw, true)
	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "Emit the command summary as JSON on stdout")

	fs.Usage = func() {
		printResumeUsage(stderr)
	}

	helpShown, err := parseCommandFlags(fs, args)
	if err != nil {
		return exitConfigError, err
	}
	if helpShown {
		return exitOK, nil
	}

	cfg, err := raw.toProcessConfig(modeResume)
	if err != nil {
		return exitConfigError, err
	}
	if cfg.ResumeFrom == "" {
		return exitConfigError, fmt.Errorf("resume requires -resume-from pointing to a previous report")
	}

	summary, exitCode, err := runProcess(ctx, cfg, encoder, stderr)
	if err != nil {
		return exitConfigError, err
	}
	if jsonOutput {
		if err := writeJSONValue(stdout, summary); err != nil {
			return exitConfigError, err
		}
	}
	return exitCode, nil
}

func runVerifyCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	runtimeCfg, err := loadRuntimeConfig(args)
	if err != nil {
		return exitConfigError, err
	}

	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := newVerifyFlagValues()
	applyVerifyFileConfig(raw, runtimeCfg.File.Verify, runtimeCfg.BaseDir)
	bindConfigFlags(fs, &runtimeCfg.Selection)
	fs.StringVar(&raw.rootDir, "dir", raw.rootDir, "Optional root directory override used to resolve source manifest entries")
	fs.StringVar(&raw.manifestPath, "manifest", raw.manifestPath, "Manifest JSON written by bulk")
	fs.StringVar(&raw.reportPath, "report", raw.reportPath, "Optional verification report path (.jsonl or .csv)")
	fs.IntVar(&raw.maxWidth, "max-width", raw.maxWidth, "Maximum allowed output width during verification")
	fs.StringVar(&raw.cpus, "cpus", raw.cpus, "Logical CPU count to use or 'auto'")
	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "Emit the command summary as JSON on stdout")

	fs.Usage = func() {
		printVerifyUsage(stderr)
	}

	helpShown, err := parseCommandFlags(fs, args)
	if err != nil {
		return exitConfigError, err
	}
	if helpShown {
		return exitOK, nil
	}
	if raw.manifestPath == "" {
		return exitConfigError, fmt.Errorf("verify requires -manifest")
	}

	cfg, err := normalizeVerifyConfig(VerifyConfig{
		RootDir:      raw.rootDir,
		ManifestPath: raw.manifestPath,
		ReportPath:   raw.reportPath,
		MaxWidth:     raw.maxWidth,
		CPUsRaw:      raw.cpus,
	})
	if err != nil {
		return exitConfigError, err
	}

	summary, err := RunVerify(ctx, cfg, stderr)
	if err != nil {
		return exitConfigError, err
	}
	if jsonOutput {
		if err := writeJSONValue(stdout, summary); err != nil {
			return exitConfigError, err
		}
	}
	return summary.ExitCode(modeVerify), nil
}

func runPlanCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	runtimeCfg, err := loadRuntimeConfig(args)
	if err != nil {
		return exitConfigError, err
	}

	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := newPlanFlagValues()
	applyPlanFileConfig(raw, runtimeCfg.File.Plan, runtimeCfg.BaseDir)
	bindConfigFlags(fs, &runtimeCfg.Selection)
	fs.StringVar(&raw.conversionManifest, "conversion-manifest", raw.conversionManifest, "Conversion manifest JSON written by bulk")
	fs.StringVar(&raw.releaseManifest, "release-manifest", raw.releaseManifest, "Public release manifest JSON to generate")
	fs.StringVar(&raw.deployPlan, "deploy-plan", raw.deployPlan, "Deploy plan JSON to generate")
	fs.StringVar(&raw.environment, "env", raw.environment, "Target environment name, for example dev/stg/prod")
	fs.StringVar(&raw.baseURL, "base-url", raw.baseURL, "Public base URL used for delivery verify targets")
	fs.StringVar(&raw.originProvider, "origin-provider", raw.originProvider, "Origin provider: local")
	fs.StringVar(&raw.originRoot, "origin-root", raw.originRoot, "Origin root directory when using -origin-provider=local")
	fs.StringVar(&raw.originPrefix, "origin-prefix", raw.originPrefix, "Optional origin object prefix")
	fs.StringVar(&raw.cdnProvider, "cdn-provider", raw.cdnProvider, "CDN provider: noop")
	fs.StringVar(&raw.immutablePrefix, "immutable-prefix", raw.immutablePrefix, "Prefix used for immutable object keys")
	fs.StringVar(&raw.mutablePrefix, "mutable-prefix", raw.mutablePrefix, "Prefix used for mutable object keys")
	fs.IntVar(&raw.verifySample, "verify-sample", raw.verifySample, "How many preferred assets to include in delivery verify checks")
	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "Emit the command summary as JSON on stdout")

	fs.Usage = func() {
		printPlanUsage(stderr)
	}

	helpShown, err := parseCommandFlags(fs, args)
	if err != nil {
		return exitConfigError, err
	}
	if helpShown {
		return exitOK, nil
	}

	cfg, err := normalizePlanConfig(PlanConfig{
		ConversionManifestPath: raw.conversionManifest,
		ReleaseManifestPath:    raw.releaseManifest,
		DeployPlanPath:         raw.deployPlan,
		Environment:            raw.environment,
		BaseURL:                raw.baseURL,
		OriginProvider:         raw.originProvider,
		OriginRoot:             raw.originRoot,
		OriginPrefix:           raw.originPrefix,
		CDNProvider:            raw.cdnProvider,
		ImmutablePrefix:        raw.immutablePrefix,
		MutablePrefix:          raw.mutablePrefix,
		VerifySample:           raw.verifySample,
	})
	if err != nil {
		return exitConfigError, err
	}

	summary, err := RunPlan(ctx, cfg, stderr)
	if err != nil {
		return exitConfigError, err
	}
	if jsonOutput {
		if err := writeJSONValue(stdout, summary); err != nil {
			return exitConfigError, err
		}
	}
	return exitOK, nil
}

func runPublishCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	runtimeCfg, err := loadRuntimeConfig(args)
	if err != nil {
		return exitConfigError, err
	}

	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := newPublishFlagValues()
	applyPublishFileConfig(raw, runtimeCfg.File.Publish, runtimeCfg.BaseDir)
	bindConfigFlags(fs, &runtimeCfg.Selection)
	fs.StringVar(&raw.planPath, "plan", raw.planPath, "Deploy plan JSON written by plan")
	fs.StringVar(&raw.dryRunMode, "dry-run", raw.dryRunMode, "Publish mode: off, plan, verify")
	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "Emit the command summary as JSON on stdout")

	fs.Usage = func() {
		printPublishUsage(stderr)
	}

	helpShown, err := parseCommandFlags(fs, args)
	if err != nil {
		return exitConfigError, err
	}
	if helpShown {
		return exitOK, nil
	}

	cfg, err := normalizePublishConfig(PublishConfig{
		PlanPath:   raw.planPath,
		DryRunMode: PublishDryRunMode(raw.dryRunMode),
	})
	if err != nil {
		return exitConfigError, err
	}

	summary, err := RunPublish(ctx, cfg, stderr)
	if err != nil {
		return exitConfigError, err
	}
	if jsonOutput {
		if err := writeJSONValue(stdout, summary); err != nil {
			return exitConfigError, err
		}
	}
	if summary.VerifyFailures > 0 {
		return exitVerifyIssue, nil
	}
	return exitOK, nil
}

func runVerifyDeliveryCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	runtimeCfg, err := loadRuntimeConfig(args)
	if err != nil {
		return exitConfigError, err
	}

	fs := flag.NewFlagSet("verify-delivery", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := newVerifyDeliveryFlagValues()
	applyVerifyDeliveryFileConfig(raw, runtimeCfg.File.VerifyDelivery, runtimeCfg.BaseDir)
	bindConfigFlags(fs, &runtimeCfg.Selection)
	fs.StringVar(&raw.planPath, "plan", raw.planPath, "Deploy plan JSON written by plan")
	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "Emit the command summary as JSON on stdout")

	fs.Usage = func() {
		printVerifyDeliveryUsage(stderr)
	}

	helpShown, err := parseCommandFlags(fs, args)
	if err != nil {
		return exitConfigError, err
	}
	if helpShown {
		return exitOK, nil
	}

	cfg, err := normalizeDeliveryVerifyConfig(DeliveryVerifyConfig{
		PlanPath: raw.planPath,
	})
	if err != nil {
		return exitConfigError, err
	}

	summary, err := RunVerifyDelivery(ctx, cfg, stderr)
	if err != nil {
		return exitConfigError, err
	}
	if jsonOutput {
		if err := writeJSONValue(stdout, summary); err != nil {
			return exitConfigError, err
		}
	}
	if summary.Failures > 0 {
		return exitVerifyIssue, nil
	}
	return exitOK, nil
}

func runInitCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := struct {
		path  string
		force bool
	}{
		path: defaultConfigFileName,
	}

	fs.StringVar(&raw.path, "path", raw.path, "Path to the config file to generate")
	fs.BoolVar(&raw.force, "force", false, "Overwrite an existing config file")
	fs.Usage = func() {
		printInitUsage(stderr)
	}

	helpShown, err := parseCommandFlags(fs, args)
	if err != nil {
		return exitConfigError, err
	}
	if helpShown {
		return exitOK, nil
	}
	if err := ctx.Err(); err != nil {
		return exitInterrupted, err
	}

	targetPath := strings.TrimSpace(raw.path)
	if targetPath == "" {
		return exitConfigError, fmt.Errorf("path cannot be empty")
	}
	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return exitConfigError, err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return exitConfigError, err
	}
	if _, err := os.Stat(absPath); err == nil && !raw.force {
		return exitConfigError, fmt.Errorf("%s already exists; rerun with -force to overwrite", absPath)
	} else if err != nil && !os.IsNotExist(err) {
		return exitConfigError, err
	}
	if err := os.WriteFile(absPath, []byte(defaultConfigTemplate()), 0o644); err != nil {
		return exitConfigError, err
	}
	writef(stderr, "Wrote %s\n", absPath)
	return exitOK, nil
}

func runProcess(ctx context.Context, cfg ProcessConfig, encoder Encoder, ui io.Writer) (Summary, int, error) {
	previousGOMAXPROCS := runtime.GOMAXPROCS(cfg.CPUs)
	defer runtime.GOMAXPROCS(previousGOMAXPROCS)

	if cfg.Mode != modeScan && !cfg.DryRun {
		if err := encoder.Check(); err != nil {
			return Summary{}, exitConfigError, err
		}
	}

	summary, err := RunProcessCommand(ctx, cfg, encoder, ui)
	if err != nil {
		return Summary{}, exitConfigError, err
	}

	return summary, summary.ExitCode(cfg.Mode), nil
}

type verifyFlagValues struct {
	rootDir      string
	manifestPath string
	reportPath   string
	maxWidth     int
	cpus         string
}

func newVerifyFlagValues() *verifyFlagValues {
	return &verifyFlagValues{
		maxWidth: 1200,
		cpus:     "auto",
	}
}

type planFlagValues struct {
	conversionManifest string
	releaseManifest    string
	deployPlan         string
	environment        string
	baseURL            string
	originProvider     string
	originRoot         string
	originPrefix       string
	cdnProvider        string
	immutablePrefix    string
	mutablePrefix      string
	verifySample       int
}

func newPlanFlagValues() *planFlagValues {
	return &planFlagValues{
		originProvider:  "local",
		cdnProvider:     "noop",
		immutablePrefix: "assets",
		mutablePrefix:   "release",
		verifySample:    3,
	}
}

type publishFlagValues struct {
	planPath   string
	dryRunMode string
}

func newPublishFlagValues() *publishFlagValues {
	return &publishFlagValues{
		dryRunMode: string(publishDryRunOff),
	}
}

type verifyDeliveryFlagValues struct {
	planPath string
}

func newVerifyDeliveryFlagValues() *verifyDeliveryFlagValues {
	return &verifyDeliveryFlagValues{}
}

type processFlagValues struct {
	rootDir        string
	outDir         string
	extensions     string
	include        stringListFlag
	exclude        stringListFlag
	includeHidden  bool
	followSymlinks bool
	maxFileSizeMB  int64
	maxPixels      int64
	maxDimension   int
	maxWidth       int
	aspectVariants string
	cropMode       string
	focusX         float64
	focusY         float64
	quality        int
	dryRun         bool
	cpus           string
	workers        string
	overwrite      bool
	onExisting     string
	reportPath     string
	manifestPath   string
	resumeFrom     string
}

func newProcessFlagValues() *processFlagValues {
	return &processFlagValues{
		rootDir:       ".",
		extensions:    "jpg,jpeg,png",
		maxFileSizeMB: 100,
		maxPixels:     80_000_000,
		maxDimension:  20_000,
		maxWidth:      1200,
		cropMode:      string(cropModeSafe),
		focusX:        0.5,
		focusY:        0.5,
		quality:       82,
		cpus:          "auto",
		workers:       "auto",
		onExisting:    string(existingSkip),
	}
}

func bindProcessFlags(fs *flag.FlagSet, raw *processFlagValues, includeBulkFlags bool) {
	fs.StringVar(&raw.rootDir, "dir", raw.rootDir, "Root directory to scan")
	fs.StringVar(&raw.extensions, "extensions", raw.extensions, "Comma-separated extensions to process (subset of jpg,jpeg,png)")
	fs.Var(newOverridingStringListValue(&raw.include), "include", "Optional include glob (repeatable or comma-separated)")
	fs.Var(newOverridingStringListValue(&raw.exclude), "exclude", "Optional exclude glob (repeatable or comma-separated)")
	fs.BoolVar(&raw.includeHidden, "include-hidden", raw.includeHidden, "Scan dot directories/files in addition to the default visible tree")
	fs.BoolVar(&raw.followSymlinks, "follow-symlinks", raw.followSymlinks, "Follow symlinked files/directories that stay under the root")
	fs.Int64Var(&raw.maxFileSizeMB, "max-file-size-mb", raw.maxFileSizeMB, "Reject files larger than this many MiB")
	fs.Int64Var(&raw.maxPixels, "max-pixels", raw.maxPixels, "Reject files whose decoded pixel count exceeds this limit")
	fs.IntVar(&raw.maxDimension, "max-dimension", raw.maxDimension, "Reject files whose width or height exceeds this limit")
	fs.StringVar(&raw.cpus, "cpus", raw.cpus, "Logical CPU count to use or 'auto'")
	fs.StringVar(&raw.reportPath, "report", raw.reportPath, "Optional report output path (.jsonl or .csv)")

	if includeBulkFlags {
		fs.IntVar(&raw.maxWidth, "max-width", raw.maxWidth, "Resize outputs down to this width, never upscale")
		fs.StringVar(&raw.aspectVariants, "aspect-variants", raw.aspectVariants, "Optional comma-separated aspect variants to generate, first becomes the primary output (for example \"16:9,4:3,1:1\")")
		fs.StringVar(&raw.cropMode, "crop-mode", raw.cropMode, "Crop strategy for aspect variants: safe, focus")
		fs.Float64Var(&raw.focusX, "focus-x", raw.focusX, "Normalized focus X used when -crop-mode=focus (0.0-1.0)")
		fs.Float64Var(&raw.focusY, "focus-y", raw.focusY, "Normalized focus Y used when -crop-mode=focus (0.0-1.0)")
		fs.IntVar(&raw.quality, "quality", raw.quality, "WebP quality (0-100)")
		fs.BoolVar(&raw.dryRun, "dry-run", raw.dryRun, "Plan the work without writing files")
		fs.StringVar(&raw.outDir, "out-dir", raw.outDir, "Optional directory used for generated .webp outputs")
		fs.StringVar(&raw.workers, "workers", raw.workers, "Worker count or 'auto'")
		fs.BoolVar(&raw.overwrite, "overwrite", raw.overwrite, "Legacy alias for -on-existing=overwrite")
		fs.StringVar(&raw.onExisting, "on-existing", raw.onExisting, "Existing output policy: skip, overwrite, fail")
		fs.StringVar(&raw.manifestPath, "manifest", raw.manifestPath, "Optional manifest JSON path for successful conversions")
		fs.StringVar(&raw.resumeFrom, "resume-from", raw.resumeFrom, "Optional previous report used to skip already completed files")
	}
}

func (raw *processFlagValues) toProcessConfig(mode CommandMode) (ProcessConfig, error) {
	rootDir, err := normalizeRoot(raw.rootDir)
	if err != nil {
		return ProcessConfig{}, err
	}
	outDir, err := normalizeOptionalDir(raw.outDir)
	if err != nil {
		return ProcessConfig{}, err
	}
	if outDir != "" && outDir == rootDir {
		return ProcessConfig{}, fmt.Errorf("out-dir must differ from dir")
	}

	extensions, extensionList, err := parseExtensions(raw.extensions)
	if err != nil {
		return ProcessConfig{}, err
	}

	includePatterns, err := compileGlobPatterns(raw.include)
	if err != nil {
		return ProcessConfig{}, err
	}
	excludePatterns, err := compileGlobPatterns(raw.exclude)
	if err != nil {
		return ProcessConfig{}, err
	}

	cpus, err := parseCPUCount(raw.cpus)
	if err != nil {
		return ProcessConfig{}, err
	}

	workers, err := parseWorkers(raw.workers, cpus)
	if err != nil {
		return ProcessConfig{}, err
	}
	if workers > cpus {
		return ProcessConfig{}, fmt.Errorf("workers (%d) cannot exceed cpus (%d)", workers, cpus)
	}

	policy, err := parseExistingPolicy(raw.onExisting, raw.overwrite)
	if err != nil {
		return ProcessConfig{}, err
	}
	aspectVariants, err := parseAspectVariants(raw.aspectVariants)
	if err != nil {
		return ProcessConfig{}, err
	}
	cropMode, err := normalizeCropMode(raw.cropMode)
	if err != nil {
		return ProcessConfig{}, err
	}

	if raw.maxFileSizeMB <= 0 {
		return ProcessConfig{}, fmt.Errorf("max-file-size-mb must be > 0")
	}
	if raw.maxPixels <= 0 {
		return ProcessConfig{}, fmt.Errorf("max-pixels must be > 0")
	}
	if raw.maxDimension <= 0 {
		return ProcessConfig{}, fmt.Errorf("max-dimension must be > 0")
	}
	if mode != modeScan {
		if raw.maxWidth <= 0 {
			return ProcessConfig{}, fmt.Errorf("max-width must be > 0")
		}
		if math.IsNaN(raw.focusX) || math.IsInf(raw.focusX, 0) || raw.focusX < 0 || raw.focusX > 1 {
			return ProcessConfig{}, fmt.Errorf("focus-x must be between 0.0 and 1.0")
		}
		if math.IsNaN(raw.focusY) || math.IsInf(raw.focusY, 0) || raw.focusY < 0 || raw.focusY > 1 {
			return ProcessConfig{}, fmt.Errorf("focus-y must be between 0.0 and 1.0")
		}
		if raw.quality < 0 || raw.quality > 100 {
			return ProcessConfig{}, fmt.Errorf("quality must be between 0 and 100")
		}
	}

	manifestPath := raw.manifestPath
	if mode == modeScan {
		manifestPath = ""
	}

	cfg := ProcessConfig{
		Mode:             mode,
		RootDir:          rootDir,
		OutDir:           outDir,
		Extensions:       extensions,
		ExtensionList:    extensionList,
		IncludePatterns:  includePatterns,
		ExcludePatterns:  excludePatterns,
		IncludeHidden:    raw.includeHidden,
		FollowSymlinks:   raw.followSymlinks,
		MaxFileSizeBytes: raw.maxFileSizeMB * 1024 * 1024,
		MaxPixels:        raw.maxPixels,
		MaxDimension:     raw.maxDimension,
		MaxWidth:         raw.maxWidth,
		AspectVariants:   aspectVariants,
		CropMode:         cropMode,
		FocusX:           raw.focusX,
		FocusY:           raw.focusY,
		Quality:          raw.quality,
		DryRun:           raw.dryRun,
		CPUs:             cpus,
		CPUsRaw:          raw.cpus,
		Workers:          workers,
		WorkersRaw:       raw.workers,
		ExistingPolicy:   policy,
		ReportPath:       raw.reportPath,
		ManifestPath:     manifestPath,
		ResumeFrom:       raw.resumeFrom,
	}
	cfg.ConfigFingerprint = processConfigFingerprint(cfg)
	return cfg, nil
}

func normalizeVerifyConfig(cfg VerifyConfig) (VerifyConfig, error) {
	if strings.TrimSpace(cfg.RootDir) != "" {
		rootDir, err := normalizeRoot(cfg.RootDir)
		if err != nil {
			return VerifyConfig{}, err
		}
		cfg.RootDir = rootDir
	}
	cpus, err := parseCPUCount(cfg.CPUsRaw)
	if err != nil {
		return VerifyConfig{}, err
	}
	if cfg.ManifestPath == "" {
		return VerifyConfig{}, fmt.Errorf("manifest is required")
	}
	if cfg.MaxWidth <= 0 {
		return VerifyConfig{}, fmt.Errorf("max-width must be > 0")
	}
	cfg.CPUs = cpus
	return cfg, nil
}

func parseCPUCount(raw string) (int, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" || value == "auto" {
		if runtime.NumCPU() < 1 {
			return 1, nil
		}
		return runtime.NumCPU(), nil
	}

	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("cpus must be a positive integer or 'auto'")
	}
	if n > runtime.NumCPU() {
		return 0, fmt.Errorf("cpus (%d) cannot exceed available logical CPUs (%d)", n, runtime.NumCPU())
	}
	return n, nil
}

func parseWorkers(raw string, cpuLimit int) (int, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" || value == "auto" {
		workers := cpuLimit
		if workers < 2 {
			return 1, nil
		}
		if workers > 8 {
			return 8, nil
		}
		return workers, nil
	}

	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("workers must be a positive integer or 'auto'")
	}
	return n, nil
}

func parseExistingPolicy(raw string, overwrite bool) (ExistingPolicy, error) {
	policy := ExistingPolicy(strings.ToLower(strings.TrimSpace(raw)))
	if overwrite {
		if policy == existingFail {
			return "", fmt.Errorf("overwrite and -on-existing=fail cannot be used together")
		}
		policy = existingOverwrite
	}

	switch policy {
	case existingSkip, existingOverwrite, existingFail:
		return policy, nil
	default:
		return "", fmt.Errorf("unsupported -on-existing policy %q", raw)
	}
}

func parseExtensions(raw string) (map[string]struct{}, []string, error) {
	items := splitCommaList([]string{raw})
	if len(items) == 0 {
		return nil, nil, fmt.Errorf("extensions cannot be empty")
	}

	set := make(map[string]struct{}, len(items))
	list := make([]string, 0, len(items))
	for _, item := range items {
		ext := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item)), ".")
		if ext == "" {
			continue
		}
		if _, ok := supportedInputExtensions[ext]; !ok {
			return nil, nil, fmt.Errorf("unsupported extension %q", item)
		}
		if _, ok := set[ext]; ok {
			continue
		}
		set[ext] = struct{}{}
		list = append(list, ext)
	}
	if len(set) == 0 {
		return nil, nil, fmt.Errorf("extensions cannot be empty")
	}
	return set, list, nil
}

func normalizeRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func normalizeOptionalDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func printRootUsage(w io.Writer) {
	writeLine(w, "webp-guard bulk scan + bulk convert CLI")
	writeLine(w)
	writeLine(w, "Usage:")
	writeLine(w, "  webp-guard help [command]")
	writeLine(w, "  webp-guard version")
	writeLine(w, "  webp-guard bulk [flags]")
	writeLine(w, "  webp-guard scan [flags]")
	writeLine(w, "  webp-guard verify -manifest ./reports/manifest.json [flags]")
	writeLine(w, "  webp-guard resume -resume-from ./reports/bulk.jsonl [flags]")
	writeLine(w, "  webp-guard plan -conversion-manifest ./out/conversion-manifest.json -release-manifest ./out/release-manifest.json -deploy-plan ./out/deploy-plan.dev.json -env dev [flags]")
	writeLine(w, "  webp-guard publish -plan ./out/deploy-plan.dev.json -dry-run=plan")
	writeLine(w, "  webp-guard verify-delivery -plan ./out/deploy-plan.dev.json")
	writeLine(w, "  webp-guard doctor [flags]")
	writeLine(w, "  webp-guard completion <shell>")
	writeLine(w, "  webp-guard init [flags]")
	writeLine(w)
	writeLine(w, "Commands:")
	writeLine(w, "  version          Print build version information")
	writeLine(w, "  bulk             Convert images into side-by-side .webp outputs")
	writeLine(w, "  scan             Run the same safety checks without writing outputs")
	writeLine(w, "  verify           Re-check conversion manifest entries on disk")
	writeLine(w, "  resume           Continue from a previous bulk report")
	writeLine(w, "  plan             Generate release and deploy artifacts")
	writeLine(w, "  publish          Upload or preview a deploy plan")
	writeLine(w, "  verify-delivery  Run read-only delivery checks from a plan")
	writeLine(w, "  doctor           Check config discovery and local runtime readiness")
	writeLine(w, "  completion       Generate shell completion scripts")
	writeLine(w, "  init             Write a starter webp-guard.toml")
	writeLine(w)
	writeLine(w, "First run:")
	writeLine(w, "  webp-guard version")
	writeLine(w, "  webp-guard scan -dir ./assets")
	writeLine(w, "  webp-guard bulk -dir ./assets -dry-run")
	writeLine(w)
	writeLine(w, "Config-first:")
	writeLine(w, "  webp-guard init")
	writeLine(w, "  After adjusting webp-guard.toml, shorter commands become possible:")
	writeLine(w, "  webp-guard bulk")
	writeLine(w, "  webp-guard verify")
	writeLine(w)
	writeLine(w, "cwebp:")
	writeLine(w, "  scan, verify, plan, publish, verify-delivery, and bulk/resume with -dry-run work before cwebp is installed.")
	writeLine(w)
	writeLine(w, "Legacy compatibility:")
	writeLine(w, "  webp-guard -dir ./assets -dry-run")
	writeLine(w)
	writeLine(w, "Config:")
	writeLine(w, "  -config ./webp-guard.toml   Load an explicit TOML config file")
	writeLine(w, "  -no-config                  Disable auto-discovery")
	writeLine(w, "  When omitted, webp-guard searches for webp-guard.toml from the cwd upward.")
	writeLine(w)
	writeLine(w, "Use 'webp-guard help <command>' or a subcommand with -h for details.")
}

func printLegacyBulkUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard [flags]")
	writeLine(w)
	writeLine(w, "This legacy form behaves like 'webp-guard bulk'.")
	writeLine(w)
	printBulkFlags(w)
}

func printBulkUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard bulk [flags]")
	writeLine(w)
	writeLine(w, "Bulk-convert jpg/jpeg/png files into side-by-side .webp outputs.")
	writeLine(w)
	printBulkFlags(w)
}

func printScanUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard scan [flags]")
	writeLine(w)
	writeLine(w, "Run safety checks without writing .webp outputs.")
	writeLine(w)
	writeLine(w, "Flags:")
	writeLine(w, "  -dir string              Root directory to scan (default \".\")")
	writeLine(w, "  -extensions string       Comma-separated extensions to inspect, subset of jpg/jpeg/png (default \"jpg,jpeg,png\")")
	writeLine(w, "  -include value           Include glob, repeatable")
	writeLine(w, "  -exclude value           Exclude glob, repeatable")
	writeLine(w, "  -include-hidden          Include dot directories/files")
	writeLine(w, "  -follow-symlinks         Follow symlinks that stay under the root")
	writeLine(w, "  -max-file-size-mb int    Reject files above this size (default 100)")
	writeLine(w, "  -max-pixels int          Reject decoded images above this pixel count (default 80000000)")
	writeLine(w, "  -max-dimension int       Reject width or height above this limit (default 20000)")
	writeLine(w, "  -cpus string             Logical CPU count to use or 'auto' (default \"auto\")")
	writeLine(w, "  -report string           Optional report path (.jsonl or .csv)")
	writeLine(w, "  -json                    Emit the command summary as JSON on stdout")
	writeLine(w, "  -config string           Optional path to webp-guard.toml")
	writeLine(w, "  -no-config               Disable config-file loading")
}

func printResumeUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard resume -resume-from ./reports/bulk.jsonl [flags]")
	writeLine(w)
	writeLine(w, "Resume a previous bulk run by skipping files that already reached a terminal state.")
	writeLine(w)
	printBulkFlags(w)
}

func printVerifyUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard verify -manifest ./reports/manifest.json [flags]")
	writeLine(w)
	writeLine(w, "Verify manifest entries against on-disk sources and generated .webp outputs.")
	writeLine(w)
	writeLine(w, "Flags:")
	writeLine(w, "  -dir string         Optional root directory override used to resolve source manifest entries")
	writeLine(w, "  -manifest string    Manifest JSON written by bulk (required)")
	writeLine(w, "  -report string      Optional verification report path (.jsonl or .csv)")
	writeLine(w, "  -max-width int      Maximum allowed output width (default 1200)")
	writeLine(w, "  -cpus string        Logical CPU count to use or 'auto' (default \"auto\")")
	writeLine(w, "  -json               Emit the command summary as JSON on stdout")
	writeLine(w, "  -config string      Optional path to webp-guard.toml")
	writeLine(w, "  -no-config          Disable config-file loading")
}

func printPlanUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard plan -conversion-manifest ./out/conversion-manifest.json -release-manifest ./out/release-manifest.json -deploy-plan ./out/deploy-plan.dev.json -env dev [flags]")
	writeLine(w)
	writeLine(w, "Generate a public release manifest and an environment-specific deploy plan.")
	writeLine(w)
	writeLine(w, "Flags:")
	writeLine(w, "  -conversion-manifest string  Conversion manifest JSON written by bulk (required)")
	writeLine(w, "  -release-manifest string     Public release manifest JSON to generate (required)")
	writeLine(w, "  -deploy-plan string          Deploy plan JSON to generate (required)")
	writeLine(w, "  -env string                  Target environment name, for example dev/stg/prod (required)")
	writeLine(w, "  -base-url string             Public base URL used for delivery verify targets")
	writeLine(w, "  -origin-provider string      Origin provider: local (default \"local\")")
	writeLine(w, "  -origin-root string          Origin root directory when using -origin-provider=local")
	writeLine(w, "  -origin-prefix string        Optional origin object prefix")
	writeLine(w, "  -cdn-provider string         CDN provider: noop (default \"noop\")")
	writeLine(w, "  -immutable-prefix string     Prefix used for immutable object keys (default \"assets\")")
	writeLine(w, "  -mutable-prefix string       Prefix used for mutable object keys (default \"release\")")
	writeLine(w, "  -verify-sample int           Number of preferred assets included in delivery verify checks (default 3)")
	writeLine(w, "  -json                        Emit the command summary as JSON on stdout")
	writeLine(w, "  -config string               Optional path to webp-guard.toml")
	writeLine(w, "  -no-config                   Disable config-file loading")
}

func printPublishUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard publish -plan ./out/deploy-plan.dev.json -dry-run=plan")
	writeLine(w)
	writeLine(w, "Execute or preview the upload/purge/verify steps described in a deploy plan.")
	writeLine(w)
	writeLine(w, "Flags:")
	writeLine(w, "  -plan string        Deploy plan JSON written by plan (required)")
	writeLine(w, "  -dry-run string     Publish mode: off, plan, verify (default \"off\")")
	writeLine(w, "  -json               Emit the command summary as JSON on stdout")
	writeLine(w, "  -config string      Optional path to webp-guard.toml")
	writeLine(w, "  -no-config          Disable config-file loading")
}

func printVerifyDeliveryUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard verify-delivery -plan ./out/deploy-plan.dev.json")
	writeLine(w)
	writeLine(w, "Run read-only delivery checks using the verify section of a deploy plan.")
	writeLine(w)
	writeLine(w, "Flags:")
	writeLine(w, "  -plan string        Deploy plan JSON written by plan (required)")
	writeLine(w, "  -json               Emit the command summary as JSON on stdout")
	writeLine(w, "  -config string      Optional path to webp-guard.toml")
	writeLine(w, "  -no-config          Disable config-file loading")
}

func printInitUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard init [flags]")
	writeLine(w)
	writeLine(w, "Generate a starter webp-guard.toml in the current project so later commands can stay short.")
	writeLine(w)
	writeLine(w, "Flags:")
	writeLine(w, "  -path string        Path to write (default \"webp-guard.toml\")")
	writeLine(w, "  -force              Overwrite an existing file")
}

func printDoctorUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard doctor [flags]")
	writeLine(w)
	writeLine(w, "Check config discovery, cwebp availability, temp-dir access, and CPU visibility.")
	writeLine(w)
	writeLine(w, "Flags:")
	writeLine(w, "  -json               Emit the doctor report as JSON on stdout")
	writeLine(w, "  -config string      Optional path to webp-guard.toml")
	writeLine(w, "  -no-config          Disable config-file loading")
}

func printCompletionUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard completion <shell>")
	writeLine(w)
	writeLine(w, "Generate a shell completion script for bash, zsh, fish, or powershell.")
	writeLine(w)
	writeLine(w, "Flags:")
	writeLine(w, "  -shell string       Shell name as an alternative to the positional argument")
	writeLine(w)
	writeLine(w, "Examples:")
	writeLine(w, "  webp-guard completion bash > ~/.local/share/bash-completion/completions/webp-guard")
	writeLine(w, "  webp-guard completion zsh > ~/.zsh/completions/_webp-guard")
	writeLine(w, "  webp-guard completion fish > ~/.config/fish/completions/webp-guard.fish")
}

func runHelpCommand(args []string, stdout, _ io.Writer) (int, error) {
	if len(args) == 0 {
		printRootUsage(stdout)
		return exitOK, nil
	}
	if len(args) > 1 {
		return exitConfigError, fmt.Errorf("help accepts at most one command name")
	}

	switch args[0] {
	case "help", "-h", "--help":
		printRootUsage(stdout)
	case "bulk":
		printBulkUsage(stdout)
	case "scan":
		printScanUsage(stdout)
	case "version":
		printVersionUsage(stdout)
	case "verify":
		printVerifyUsage(stdout)
	case "resume":
		printResumeUsage(stdout)
	case "plan":
		printPlanUsage(stdout)
	case "publish":
		printPublishUsage(stdout)
	case "verify-delivery":
		printVerifyDeliveryUsage(stdout)
	case "init":
		printInitUsage(stdout)
	case "doctor":
		printDoctorUsage(stdout)
	case "completion":
		printCompletionUsage(stdout)
	default:
		return exitConfigError, fmt.Errorf("unknown help topic %q", args[0])
	}
	return exitOK, nil
}

func printBulkFlags(w io.Writer) {
	writeLine(w, "Flags:")
	writeLine(w, "  -dir string              Root directory to scan (default \".\")")
	writeLine(w, "  -extensions string       Comma-separated extensions to process, subset of jpg/jpeg/png (default \"jpg,jpeg,png\")")
	writeLine(w, "  -include value           Include glob, repeatable")
	writeLine(w, "  -exclude value           Exclude glob, repeatable")
	writeLine(w, "  -include-hidden          Include dot directories/files")
	writeLine(w, "  -follow-symlinks         Follow symlinks that stay under the root")
	writeLine(w, "  -max-file-size-mb int    Reject files above this size (default 100)")
	writeLine(w, "  -max-pixels int          Reject decoded images above this pixel count (default 80000000)")
	writeLine(w, "  -max-dimension int       Reject width or height above this limit (default 20000)")
	writeLine(w, "  -cpus string             Logical CPU count to use or 'auto' (default \"auto\")")
	writeLine(w, "  -max-width int           Resize outputs down to this width, never upscale (default 1200)")
	writeLine(w, "  -aspect-variants string  Optional comma-separated aspect variants, first becomes primary output")
	writeLine(w, "  -crop-mode string        Crop strategy for aspect variants: safe, focus (default \"safe\")")
	writeLine(w, "  -focus-x float           Normalized focus X for -crop-mode=focus (default 0.5)")
	writeLine(w, "  -focus-y float           Normalized focus Y for -crop-mode=focus (default 0.5)")
	writeLine(w, "  -quality int             WebP quality 0-100 (default 82)")
	writeLine(w, "  -workers string          Worker count or 'auto' (default \"auto\")")
	writeLine(w, "  -dry-run                 Plan the work without writing files")
	writeLine(w, "  -out-dir string          Optional directory used for generated .webp outputs")
	writeLine(w, "  -overwrite               Legacy alias for -on-existing=overwrite")
	writeLine(w, "  -on-existing string      Existing output policy: skip, overwrite, fail (default \"skip\")")
	writeLine(w, "  -report string           Optional report path (.jsonl or .csv)")
	writeLine(w, "  -manifest string         Optional manifest JSON path for successful conversions")
	writeLine(w, "  -resume-from string      Optional previous report used to skip already completed files")
	writeLine(w, "  -json                    Emit the command summary as JSON on stdout")
	writeLine(w, "  -config string           Optional path to webp-guard.toml")
	writeLine(w, "  -no-config               Disable config-file loading")
}
