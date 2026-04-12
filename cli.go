package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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

func (s *stringListFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringListFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func runBulkCommand(ctx context.Context, args []string, encoder Encoder, stdout, stderr io.Writer, legacy bool) (int, error) {
	fs := flag.NewFlagSet("bulk", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := newProcessFlagValues()
	bindProcessFlags(fs, raw, true)

	if legacy {
		fs.Usage = func() {
			printLegacyBulkUsage(stderr)
		}
	} else {
		fs.Usage = func() {
			printBulkUsage(stderr)
		}
	}

	if err := fs.Parse(args); err != nil {
		return exitConfigError, err
	}

	cfg, err := raw.toProcessConfig(modeBulk)
	if err != nil {
		return exitConfigError, err
	}

	return runProcess(ctx, cfg, encoder, stdout)
}

func runScanCommand(ctx context.Context, args []string, encoder Encoder, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := newProcessFlagValues()
	bindProcessFlags(fs, raw, false)

	fs.Usage = func() {
		printScanUsage(stderr)
	}

	if err := fs.Parse(args); err != nil {
		return exitConfigError, err
	}

	cfg, err := raw.toProcessConfig(modeScan)
	if err != nil {
		return exitConfigError, err
	}

	return runProcess(ctx, cfg, encoder, stdout)
}

func runResumeCommand(ctx context.Context, args []string, encoder Encoder, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := newProcessFlagValues()
	bindProcessFlags(fs, raw, true)

	fs.Usage = func() {
		printResumeUsage(stderr)
	}

	if err := fs.Parse(args); err != nil {
		return exitConfigError, err
	}

	cfg, err := raw.toProcessConfig(modeResume)
	if err != nil {
		return exitConfigError, err
	}
	if cfg.ResumeFrom == "" {
		return exitConfigError, fmt.Errorf("resume requires -resume-from pointing to a previous report")
	}

	return runProcess(ctx, cfg, encoder, stdout)
}

func runVerifyCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := struct {
		rootDir      string
		manifestPath string
		reportPath   string
		maxWidth     int
		cpus         string
	}{
		maxWidth: 1200,
		cpus:     "auto",
	}

	fs.StringVar(&raw.rootDir, "dir", "", "Optional root directory override used to resolve source manifest entries")
	fs.StringVar(&raw.manifestPath, "manifest", "", "Manifest JSON written by bulk")
	fs.StringVar(&raw.reportPath, "report", "", "Optional verification report path (.jsonl or .csv)")
	fs.IntVar(&raw.maxWidth, "max-width", 1200, "Maximum allowed output width during verification")
	fs.StringVar(&raw.cpus, "cpus", "auto", "Logical CPU count to use or 'auto'")

	fs.Usage = func() {
		printVerifyUsage(stderr)
	}

	if err := fs.Parse(args); err != nil {
		return exitConfigError, err
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

	summary, err := RunVerify(ctx, cfg, stdout)
	if err != nil {
		return exitConfigError, err
	}
	return summary.ExitCode(modeVerify), nil
}

func runPlanCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := struct {
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
	}{
		originProvider:  "local",
		cdnProvider:     "noop",
		immutablePrefix: "assets",
		mutablePrefix:   "release",
		verifySample:    3,
	}

	fs.StringVar(&raw.conversionManifest, "conversion-manifest", "", "Conversion manifest JSON written by bulk")
	fs.StringVar(&raw.releaseManifest, "release-manifest", "", "Public release manifest JSON to generate")
	fs.StringVar(&raw.deployPlan, "deploy-plan", "", "Deploy plan JSON to generate")
	fs.StringVar(&raw.environment, "env", "", "Target environment name, for example dev/stg/prod")
	fs.StringVar(&raw.baseURL, "base-url", "", "Public base URL used for delivery verify targets")
	fs.StringVar(&raw.originProvider, "origin-provider", "local", "Origin provider: local")
	fs.StringVar(&raw.originRoot, "origin-root", "", "Origin root directory when using -origin-provider=local")
	fs.StringVar(&raw.originPrefix, "origin-prefix", "", "Optional origin object prefix")
	fs.StringVar(&raw.cdnProvider, "cdn-provider", "noop", "CDN provider: noop")
	fs.StringVar(&raw.immutablePrefix, "immutable-prefix", "assets", "Prefix used for immutable object keys")
	fs.StringVar(&raw.mutablePrefix, "mutable-prefix", "release", "Prefix used for mutable object keys")
	fs.IntVar(&raw.verifySample, "verify-sample", 3, "How many preferred assets to include in delivery verify checks")

	fs.Usage = func() {
		printPlanUsage(stderr)
	}

	if err := fs.Parse(args); err != nil {
		return exitConfigError, err
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

	if err := RunPlan(ctx, cfg, stdout); err != nil {
		return exitConfigError, err
	}
	return exitOK, nil
}

func runPublishCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := struct {
		planPath   string
		dryRunMode string
	}{
		dryRunMode: string(publishDryRunOff),
	}

	fs.StringVar(&raw.planPath, "plan", "", "Deploy plan JSON written by plan")
	fs.StringVar(&raw.dryRunMode, "dry-run", string(publishDryRunOff), "Publish mode: off, plan, verify")

	fs.Usage = func() {
		printPublishUsage(stderr)
	}

	if err := fs.Parse(args); err != nil {
		return exitConfigError, err
	}

	cfg, err := normalizePublishConfig(PublishConfig{
		PlanPath:   raw.planPath,
		DryRunMode: PublishDryRunMode(raw.dryRunMode),
	})
	if err != nil {
		return exitConfigError, err
	}

	summary, err := RunPublish(ctx, cfg, stdout)
	if err != nil {
		return exitConfigError, err
	}
	if summary.VerifyFailures > 0 {
		return exitVerifyIssue, nil
	}
	return exitOK, nil
}

func runVerifyDeliveryCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("verify-delivery", flag.ContinueOnError)
	fs.SetOutput(stderr)

	raw := struct {
		planPath string
	}{
		planPath: "",
	}

	fs.StringVar(&raw.planPath, "plan", "", "Deploy plan JSON written by plan")

	fs.Usage = func() {
		printVerifyDeliveryUsage(stderr)
	}

	if err := fs.Parse(args); err != nil {
		return exitConfigError, err
	}

	cfg, err := normalizeDeliveryVerifyConfig(DeliveryVerifyConfig{
		PlanPath: raw.planPath,
	})
	if err != nil {
		return exitConfigError, err
	}

	summary, err := RunVerifyDelivery(ctx, cfg, stdout)
	if err != nil {
		return exitConfigError, err
	}
	if summary.Failures > 0 {
		return exitVerifyIssue, nil
	}
	return exitOK, nil
}

func runProcess(ctx context.Context, cfg ProcessConfig, encoder Encoder, stdout io.Writer) (int, error) {
	previousGOMAXPROCS := runtime.GOMAXPROCS(cfg.CPUs)
	defer runtime.GOMAXPROCS(previousGOMAXPROCS)

	if cfg.Mode != modeScan && !cfg.DryRun {
		if err := encoder.Check(); err != nil {
			return exitConfigError, err
		}
	}

	summary, err := RunProcessCommand(ctx, cfg, encoder, stdout)
	if err != nil {
		return exitConfigError, err
	}

	return summary.ExitCode(cfg.Mode), nil
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
	fs.StringVar(&raw.rootDir, "dir", ".", "Root directory to scan")
	fs.StringVar(&raw.extensions, "extensions", "jpg,jpeg,png", "Comma-separated extensions to process (subset of jpg,jpeg,png)")
	fs.Var(&raw.include, "include", "Optional include glob (repeatable or comma-separated)")
	fs.Var(&raw.exclude, "exclude", "Optional exclude glob (repeatable or comma-separated)")
	fs.BoolVar(&raw.includeHidden, "include-hidden", false, "Scan dot directories/files in addition to the default visible tree")
	fs.BoolVar(&raw.followSymlinks, "follow-symlinks", false, "Follow symlinked files/directories that stay under the root")
	fs.Int64Var(&raw.maxFileSizeMB, "max-file-size-mb", 100, "Reject files larger than this many MiB")
	fs.Int64Var(&raw.maxPixels, "max-pixels", 80_000_000, "Reject files whose decoded pixel count exceeds this limit")
	fs.IntVar(&raw.maxDimension, "max-dimension", 20_000, "Reject files whose width or height exceeds this limit")
	fs.StringVar(&raw.cpus, "cpus", "auto", "Logical CPU count to use or 'auto'")
	fs.StringVar(&raw.reportPath, "report", "", "Optional report output path (.jsonl or .csv)")

	if includeBulkFlags {
		fs.IntVar(&raw.maxWidth, "max-width", 1200, "Resize outputs down to this width, never upscale")
		fs.StringVar(&raw.aspectVariants, "aspect-variants", "", "Optional comma-separated aspect variants to generate, first becomes the primary output (for example \"16:9,4:3,1:1\")")
		fs.StringVar(&raw.cropMode, "crop-mode", string(cropModeSafe), "Crop strategy for aspect variants: safe, focus")
		fs.Float64Var(&raw.focusX, "focus-x", 0.5, "Normalized focus X used when -crop-mode=focus (0.0-1.0)")
		fs.Float64Var(&raw.focusY, "focus-y", 0.5, "Normalized focus Y used when -crop-mode=focus (0.0-1.0)")
		fs.IntVar(&raw.quality, "quality", 82, "WebP quality (0-100)")
		fs.BoolVar(&raw.dryRun, "dry-run", false, "Plan the work without writing files")
		fs.StringVar(&raw.outDir, "out-dir", "", "Optional directory used for generated .webp outputs")
		fs.StringVar(&raw.workers, "workers", "auto", "Worker count or 'auto'")
		fs.BoolVar(&raw.overwrite, "overwrite", false, "Legacy alias for -on-existing=overwrite")
		fs.StringVar(&raw.onExisting, "on-existing", string(existingSkip), "Existing output policy: skip, overwrite, fail")
		fs.StringVar(&raw.manifestPath, "manifest", "", "Optional manifest JSON path for successful conversions")
		fs.StringVar(&raw.resumeFrom, "resume-from", "", "Optional previous report used to skip already completed files")
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
		if raw.focusX < 0 || raw.focusX > 1 {
			return ProcessConfig{}, fmt.Errorf("focus-x must be between 0.0 and 1.0")
		}
		if raw.focusY < 0 || raw.focusY > 1 {
			return ProcessConfig{}, fmt.Errorf("focus-y must be between 0.0 and 1.0")
		}
		if raw.quality < 0 || raw.quality > 100 {
			return ProcessConfig{}, fmt.Errorf("quality must be between 0 and 100")
		}
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
		ManifestPath:     raw.manifestPath,
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
	writeLine(w, "  webp-guard bulk [flags]")
	writeLine(w, "  webp-guard scan [flags]")
	writeLine(w, "  webp-guard verify -manifest ./reports/manifest.json [flags]")
	writeLine(w, "  webp-guard resume -resume-from ./reports/bulk.jsonl [flags]")
	writeLine(w, "  webp-guard plan -conversion-manifest ./out/conversion-manifest.json -release-manifest ./out/release-manifest.json -deploy-plan ./out/deploy-plan.dev.json -env dev [flags]")
	writeLine(w, "  webp-guard publish -plan ./out/deploy-plan.dev.json -dry-run=plan")
	writeLine(w, "  webp-guard verify-delivery -plan ./out/deploy-plan.dev.json")
	writeLine(w)
	writeLine(w, "Legacy compatibility:")
	writeLine(w, "  webp-guard -dir ./assets -dry-run")
	writeLine(w)
	writeLine(w, "Run 'webp-guard help' for this summary or use a subcommand with -h.")
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
}

func printPublishUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard publish -plan ./out/deploy-plan.dev.json -dry-run=plan")
	writeLine(w)
	writeLine(w, "Execute or preview the upload/purge/verify steps described in a deploy plan.")
	writeLine(w)
	writeLine(w, "Flags:")
	writeLine(w, "  -plan string        Deploy plan JSON written by plan (required)")
	writeLine(w, "  -dry-run string     Publish mode: off, plan, verify (default \"off\")")
}

func printVerifyDeliveryUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard verify-delivery -plan ./out/deploy-plan.dev.json")
	writeLine(w)
	writeLine(w, "Run read-only delivery checks using the verify section of a deploy plan.")
	writeLine(w)
	writeLine(w, "Flags:")
	writeLine(w, "  -plan string        Deploy plan JSON written by plan (required)")
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
}
