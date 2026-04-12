package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	defaultConfigFileName = "webp-guard.toml"
	configSchemaVersion   = 1
)

type configSelection struct {
	Path       string
	NoConfig   bool
	Discovered bool
}

type runtimeConfig struct {
	Selection configSelection
	File      fileConfig
	BaseDir   string
}

type fileConfig struct {
	SchemaVersion  int                      `toml:"schema_version"`
	Process        processFileConfig        `toml:"process"`
	Bulk           processFileConfig        `toml:"bulk"`
	Scan           processFileConfig        `toml:"scan"`
	Resume         processFileConfig        `toml:"resume"`
	Verify         verifyFileConfig         `toml:"verify"`
	Plan           planFileConfig           `toml:"plan"`
	Publish        publishFileConfig        `toml:"publish"`
	VerifyDelivery verifyDeliveryFileConfig `toml:"verify_delivery"`
}

type processFileConfig struct {
	Dir            *string  `toml:"dir"`
	OutDir         *string  `toml:"out_dir"`
	Extensions     []string `toml:"extensions"`
	Include        []string `toml:"include"`
	Exclude        []string `toml:"exclude"`
	IncludeHidden  *bool    `toml:"include_hidden"`
	FollowSymlinks *bool    `toml:"follow_symlinks"`
	MaxFileSizeMB  *int64   `toml:"max_file_size_mb"`
	MaxPixels      *int64   `toml:"max_pixels"`
	MaxDimension   *int     `toml:"max_dimension"`
	MaxWidth       *int     `toml:"max_width"`
	AspectVariants []string `toml:"aspect_variants"`
	CropMode       *string  `toml:"crop_mode"`
	FocusX         *float64 `toml:"focus_x"`
	FocusY         *float64 `toml:"focus_y"`
	Quality        *int     `toml:"quality"`
	DryRun         *bool    `toml:"dry_run"`
	CPUs           *string  `toml:"cpus"`
	Workers        *string  `toml:"workers"`
	OnExisting     *string  `toml:"on_existing"`
	Report         *string  `toml:"report"`
	Manifest       *string  `toml:"manifest"`
	ResumeFrom     *string  `toml:"resume_from"`
}

type verifyFileConfig struct {
	Dir      *string `toml:"dir"`
	Manifest *string `toml:"manifest"`
	Report   *string `toml:"report"`
	MaxWidth *int    `toml:"max_width"`
	CPUs     *string `toml:"cpus"`
}

type planFileConfig struct {
	ConversionManifest *string `toml:"conversion_manifest"`
	ReleaseManifest    *string `toml:"release_manifest"`
	DeployPlan         *string `toml:"deploy_plan"`
	Environment        *string `toml:"env"`
	BaseURL            *string `toml:"base_url"`
	OriginProvider     *string `toml:"origin_provider"`
	OriginRoot         *string `toml:"origin_root"`
	OriginPrefix       *string `toml:"origin_prefix"`
	CDNProvider        *string `toml:"cdn_provider"`
	ImmutablePrefix    *string `toml:"immutable_prefix"`
	MutablePrefix      *string `toml:"mutable_prefix"`
	VerifySample       *int    `toml:"verify_sample"`
}

type publishFileConfig struct {
	Plan   *string `toml:"plan"`
	DryRun *string `toml:"dry_run"`
}

type verifyDeliveryFileConfig struct {
	Plan *string `toml:"plan"`
}

func loadRuntimeConfig(args []string) (runtimeConfig, error) {
	selection, err := detectConfigSelection(args)
	if err != nil {
		return runtimeConfig{}, err
	}
	if argsRequestHelp(args) {
		return runtimeConfig{Selection: selection}, nil
	}
	if selection.NoConfig {
		return runtimeConfig{Selection: selection}, nil
	}

	path := selection.Path
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return runtimeConfig{}, err
		}
		discoveredPath, found, err := discoverConfigFile(cwd)
		if err != nil {
			return runtimeConfig{}, err
		}
		if !found {
			return runtimeConfig{Selection: selection}, nil
		}
		path = discoveredPath
		selection.Path = discoveredPath
		selection.Discovered = true
	} else {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return runtimeConfig{}, err
		}
		path = absPath
		selection.Path = absPath
	}

	fileCfg, err := readConfigFile(path)
	if err != nil {
		return runtimeConfig{}, err
	}
	return runtimeConfig{
		Selection: selection,
		File:      fileCfg,
		BaseDir:   filepath.Dir(path),
	}, nil
}

func detectConfigSelection(args []string) (configSelection, error) {
	selection := configSelection{}

	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}

		switch {
		case arg == "-no-config" || arg == "--no-config":
			selection.NoConfig = true
		case strings.HasPrefix(arg, "-no-config="):
			value, err := strconv.ParseBool(strings.TrimSpace(strings.TrimPrefix(arg, "-no-config=")))
			if err != nil {
				return configSelection{}, fmt.Errorf("invalid boolean for -no-config: %w", err)
			}
			selection.NoConfig = value
		case strings.HasPrefix(arg, "--no-config="):
			value, err := strconv.ParseBool(strings.TrimSpace(strings.TrimPrefix(arg, "--no-config=")))
			if err != nil {
				return configSelection{}, fmt.Errorf("invalid boolean for --no-config: %w", err)
			}
			selection.NoConfig = value
		case arg == "-config" || arg == "--config":
			if index+1 >= len(args) {
				return configSelection{}, fmt.Errorf("%s requires a path", arg)
			}
			index++
			selection.Path = strings.TrimSpace(args[index])
		case strings.HasPrefix(arg, "-config="):
			selection.Path = strings.TrimSpace(strings.TrimPrefix(arg, "-config="))
		case strings.HasPrefix(arg, "--config="):
			selection.Path = strings.TrimSpace(strings.TrimPrefix(arg, "--config="))
		}
	}

	if selection.NoConfig && selection.Path != "" {
		return configSelection{}, fmt.Errorf("-config and -no-config cannot be used together")
	}
	if selection.Path == "" {
		return selection, nil
	}
	if selection.Path == "-" {
		return configSelection{}, fmt.Errorf("-config does not support stdin")
	}
	return selection, nil
}

func argsRequestHelp(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return true
		}
	}
	return false
}

func discoverConfigFile(startDir string) (string, bool, error) {
	dir := filepath.Clean(startDir)
	for {
		candidate := filepath.Join(dir, defaultConfigFileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", false, err
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}

func readConfigFile(path string) (fileConfig, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileConfig{}, err
	}
	if info.IsDir() {
		return fileConfig{}, fmt.Errorf("config path %s is a directory", path)
	}

	var cfg fileConfig
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return fileConfig{}, err
	}
	if cfg.SchemaVersion != 0 && cfg.SchemaVersion != configSchemaVersion {
		return fileConfig{}, fmt.Errorf("unsupported config schema_version %d", cfg.SchemaVersion)
	}
	return cfg, nil
}

func bindConfigFlags(fs configFlagBinder, selection *configSelection) {
	defaultPath := ""
	if selection.Path != "" && !selection.Discovered {
		defaultPath = selection.Path
	}
	fs.StringVar(&selection.Path, "config", defaultPath, "Optional path to webp-guard.toml (auto-discovered from cwd upward when omitted)")
	fs.BoolVar(&selection.NoConfig, "no-config", selection.NoConfig, "Disable config-file loading")
}

type configFlagBinder interface {
	StringVar(p *string, name string, value string, usage string)
	BoolVar(p *bool, name string, value bool, usage string)
}

func applyProcessFileConfig(raw *processFlagValues, cfg processFileConfig, baseDir string) {
	if cfg.Dir != nil {
		raw.rootDir = resolveConfigPath(baseDir, *cfg.Dir)
	}
	if cfg.OutDir != nil {
		raw.outDir = resolveConfigPath(baseDir, *cfg.OutDir)
	}
	if cfg.Extensions != nil {
		raw.extensions = strings.Join(cfg.Extensions, ",")
	}
	if cfg.Include != nil {
		raw.include = append(raw.include[:0], cfg.Include...)
	}
	if cfg.Exclude != nil {
		raw.exclude = append(raw.exclude[:0], cfg.Exclude...)
	}
	if cfg.IncludeHidden != nil {
		raw.includeHidden = *cfg.IncludeHidden
	}
	if cfg.FollowSymlinks != nil {
		raw.followSymlinks = *cfg.FollowSymlinks
	}
	if cfg.MaxFileSizeMB != nil {
		raw.maxFileSizeMB = *cfg.MaxFileSizeMB
	}
	if cfg.MaxPixels != nil {
		raw.maxPixels = *cfg.MaxPixels
	}
	if cfg.MaxDimension != nil {
		raw.maxDimension = *cfg.MaxDimension
	}
	if cfg.MaxWidth != nil {
		raw.maxWidth = *cfg.MaxWidth
	}
	if cfg.AspectVariants != nil {
		raw.aspectVariants = strings.Join(cfg.AspectVariants, ",")
	}
	if cfg.CropMode != nil {
		raw.cropMode = *cfg.CropMode
	}
	if cfg.FocusX != nil {
		raw.focusX = *cfg.FocusX
	}
	if cfg.FocusY != nil {
		raw.focusY = *cfg.FocusY
	}
	if cfg.Quality != nil {
		raw.quality = *cfg.Quality
	}
	if cfg.DryRun != nil {
		raw.dryRun = *cfg.DryRun
	}
	if cfg.CPUs != nil {
		raw.cpus = *cfg.CPUs
	}
	if cfg.Workers != nil {
		raw.workers = *cfg.Workers
	}
	if cfg.OnExisting != nil {
		raw.onExisting = *cfg.OnExisting
	}
	if cfg.Report != nil {
		raw.reportPath = resolveConfigPath(baseDir, *cfg.Report)
	}
	if cfg.Manifest != nil {
		raw.manifestPath = resolveConfigPath(baseDir, *cfg.Manifest)
	}
	if cfg.ResumeFrom != nil {
		raw.resumeFrom = resolveConfigPath(baseDir, *cfg.ResumeFrom)
	}
}

func applyVerifyFileConfig(raw *verifyFlagValues, cfg verifyFileConfig, baseDir string) {
	if cfg.Dir != nil {
		raw.rootDir = resolveConfigPath(baseDir, *cfg.Dir)
	}
	if cfg.Manifest != nil {
		raw.manifestPath = resolveConfigPath(baseDir, *cfg.Manifest)
	}
	if cfg.Report != nil {
		raw.reportPath = resolveConfigPath(baseDir, *cfg.Report)
	}
	if cfg.MaxWidth != nil {
		raw.maxWidth = *cfg.MaxWidth
	}
	if cfg.CPUs != nil {
		raw.cpus = *cfg.CPUs
	}
}

func applyPlanFileConfig(raw *planFlagValues, cfg planFileConfig, baseDir string) {
	if cfg.ConversionManifest != nil {
		raw.conversionManifest = resolveConfigPath(baseDir, *cfg.ConversionManifest)
	}
	if cfg.ReleaseManifest != nil {
		raw.releaseManifest = resolveConfigPath(baseDir, *cfg.ReleaseManifest)
	}
	if cfg.DeployPlan != nil {
		raw.deployPlan = resolveConfigPath(baseDir, *cfg.DeployPlan)
	}
	if cfg.Environment != nil {
		raw.environment = *cfg.Environment
	}
	if cfg.BaseURL != nil {
		raw.baseURL = *cfg.BaseURL
	}
	if cfg.OriginProvider != nil {
		raw.originProvider = *cfg.OriginProvider
	}
	if cfg.OriginRoot != nil {
		raw.originRoot = resolveConfigPath(baseDir, *cfg.OriginRoot)
	}
	if cfg.OriginPrefix != nil {
		raw.originPrefix = *cfg.OriginPrefix
	}
	if cfg.CDNProvider != nil {
		raw.cdnProvider = *cfg.CDNProvider
	}
	if cfg.ImmutablePrefix != nil {
		raw.immutablePrefix = *cfg.ImmutablePrefix
	}
	if cfg.MutablePrefix != nil {
		raw.mutablePrefix = *cfg.MutablePrefix
	}
	if cfg.VerifySample != nil {
		raw.verifySample = *cfg.VerifySample
	}
}

func applyPublishFileConfig(raw *publishFlagValues, cfg publishFileConfig, baseDir string) {
	if cfg.Plan != nil {
		raw.planPath = resolveConfigPath(baseDir, *cfg.Plan)
	}
	if cfg.DryRun != nil {
		raw.dryRunMode = *cfg.DryRun
	}
}

func applyVerifyDeliveryFileConfig(raw *verifyDeliveryFlagValues, cfg verifyDeliveryFileConfig, baseDir string) {
	if cfg.Plan != nil {
		raw.planPath = resolveConfigPath(baseDir, *cfg.Plan)
	}
}

func resolveConfigPath(baseDir string, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(trimmed)))
}

func defaultConfigTemplate() string {
	return `schema_version = 1

[process]
dir = "./assets"
out_dir = "./out/assets"
extensions = ["jpg", "jpeg", "png"]
cpus = "auto"
workers = "auto"
max_file_size_mb = 100
max_pixels = 80000000
max_dimension = 20000
report = "./out/conversion-report.jsonl"

[bulk]
max_width = 1200
quality = 82
on_existing = "skip"
manifest = "./out/conversion-manifest.json"
# aspect_variants = ["16:9", "4:3", "1:1"]
# crop_mode = "safe"
# focus_x = 0.5
# focus_y = 0.5

[verify]
manifest = "./out/conversion-manifest.json"
report = "./out/verify-report.jsonl"
max_width = 1200
cpus = "auto"

[plan]
conversion_manifest = "./out/conversion-manifest.json"
release_manifest = "./out/release-manifest.json"
deploy_plan = "./out/deploy-plan.dev.json"
env = "dev"
origin_provider = "local"
origin_root = "./out/dev-origin"
immutable_prefix = "assets"
mutable_prefix = "release"
verify_sample = 3

[publish]
plan = "./out/deploy-plan.dev.json"
dry_run = "plan"

[verify_delivery]
plan = "./out/deploy-plan.dev.json"
`
}
