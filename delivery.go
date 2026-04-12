package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	releaseManifestSchemaVersion = 1
	deployPlanSchemaVersion      = 1
	immutableCacheControl        = "public, max-age=31536000, immutable"
	mutableCacheControl          = "public, max-age=60, stale-while-revalidate=300"
	defaultManifestObjectName    = "release-manifest.json"
)

type PublishDryRunMode string

const (
	publishDryRunOff    PublishDryRunMode = "off"
	publishDryRunPlan   PublishDryRunMode = "plan"
	publishDryRunVerify PublishDryRunMode = "verify"
)

type PlanConfig struct {
	ConversionManifestPath string
	ReleaseManifestPath    string
	DeployPlanPath         string
	Environment            string
	BaseURL                string
	OriginProvider         string
	OriginRoot             string
	OriginPrefix           string
	CDNProvider            string
	ImmutablePrefix        string
	MutablePrefix          string
	VerifySample           int
}

type PublishConfig struct {
	PlanPath   string
	DryRunMode PublishDryRunMode
}

type DeliveryVerifyConfig struct {
	PlanPath string
}

type PublishSummary struct {
	Environment      string
	ImmutableUploads int
	MutableUploads   int
	SkippedUploads   int
	PurgeRequests    int
	VerifyChecks     int
	VerifyFailures   int
}

type DeliveryVerifySummary struct {
	Total    int
	Verified int
	Failures int
}

type PlanSummary struct {
	Environment         string `json:"environment"`
	ReleaseManifestPath string `json:"release_manifest_path"`
	DeployPlanPath      string `json:"deploy_plan_path"`
	Assets              int    `json:"assets"`
	ImmutableUploads    int    `json:"immutable_uploads"`
	MutableUploads      int    `json:"mutable_uploads"`
	VerifyChecks        int    `json:"verify_checks"`
}

type ConversionManifest struct {
	Version     int             `json:"version"`
	GeneratedAt string          `json:"generated_at"`
	Command     string          `json:"command"`
	RootDir     string          `json:"root_dir"`
	OutputDir   string          `json:"output_dir,omitempty"`
	Entries     []ManifestEntry `json:"entries"`
	Summary     Summary         `json:"summary"`

	manifestPath string `json:"-"`
}

type ReleaseManifest struct {
	SchemaVersion int                    `json:"schema_version"`
	GeneratedAt   string                 `json:"generated_at"`
	Build         ReleaseBuildInfo       `json:"build"`
	Assets        []ReleaseAsset         `json:"assets"`
	Mutable       ReleaseMutableMetadata `json:"mutable"`
}

type ReleaseBuildInfo struct {
	Encoder  ReleaseEncoderInfo `json:"encoder"`
	Quality  int                `json:"quality,omitempty"`
	MaxWidth int                `json:"max_width,omitempty"`
}

type ReleaseEncoderInfo struct {
	Name string `json:"name"`
}

type ReleaseMutableMetadata struct {
	ManifestKey  string `json:"manifest_key"`
	CacheControl string `json:"cache_control"`
	ContentType  string `json:"content_type"`
}

type ReleaseAsset struct {
	LogicalID       string           `json:"logical_id"`
	LogicalPath     string           `json:"logical_path"`
	Source          ReleaseSource    `json:"source"`
	Variants        []ReleaseVariant `json:"variants"`
	FallbackFormat  string           `json:"fallback_format"`
	PreferredFormat string           `json:"preferred_format"`
}

type ReleaseSource struct {
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	ContentType string `json:"content_type"`
}

type ReleaseVariant struct {
	Name         string `json:"name,omitempty"`
	Usage        string `json:"usage,omitempty"`
	AspectRatio  string `json:"aspect_ratio,omitempty"`
	Format       string `json:"format"`
	ContentHash  string `json:"content_hash"`
	ObjectKey    string `json:"object_key"`
	PublicPath   string `json:"public_path,omitempty"`
	Bytes        int64  `json:"bytes"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	ContentType  string `json:"content_type"`
	CacheControl string `json:"cache_control"`
}

type DeployPlan struct {
	SchemaVersion  int                `json:"schema_version"`
	GeneratedAt    string             `json:"generated_at"`
	Environment    string             `json:"environment"`
	Release        DeployPlanRelease  `json:"release"`
	Origin         OriginTarget       `json:"origin"`
	CDN            CDNTarget          `json:"cdn"`
	Uploads        []UploadRequest    `json:"uploads"`
	MutableUploads []UploadRequest    `json:"mutable_uploads"`
	Purge          PurgeRequest       `json:"purge"`
	Verify         DeliveryVerifyPlan `json:"verify"`

	baseDir string `json:"-"`
}

type DeployPlanRelease struct {
	ReleaseManifestPath    string `json:"release_manifest_path"`
	ConversionManifestPath string `json:"conversion_manifest_path,omitempty"`
}

type OriginTarget struct {
	Provider string `json:"provider"`
	RootDir  string `json:"root_dir,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
}

type CDNTarget struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url,omitempty"`
}

type UploadRequest struct {
	LocalPath      string            `json:"local_path"`
	ObjectKey      string            `json:"object_key"`
	ContentType    string            `json:"content_type"`
	CacheControl   string            `json:"cache_control"`
	ContentSHA256  string            `json:"content_sha256"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Immutable      bool              `json:"immutable"`
	SkipIfSameHash bool              `json:"skip_if_same_hash"`
}

type PurgeRequest struct {
	Enabled bool     `json:"enabled"`
	URLs    []string `json:"urls,omitempty"`
}

type DeliveryVerifyPlan struct {
	Enabled bool          `json:"enabled"`
	Checks  []VerifyCheck `json:"checks"`
}

type VerifyCheck struct {
	URL                string `json:"url,omitempty"`
	ObjectKey          string `json:"object_key,omitempty"`
	ExpectStatus       int    `json:"expect_status"`
	ExpectContentType  string `json:"expect_content_type,omitempty"`
	ExpectCacheControl string `json:"expect_cache_control,omitempty"`
	ExpectSHA256       string `json:"expect_sha256,omitempty"`
}

type OriginAdapter interface {
	Stat(ctx context.Context, key string) (ObjectMeta, error)
	Upload(ctx context.Context, req UploadRequest) (bool, error)
}

type ObjectMeta struct {
	Key    string
	Size   int64
	SHA256 string
}

type LocalOriginAdapter struct {
	RootDir string
}

func normalizePlanConfig(cfg PlanConfig) (PlanConfig, error) {
	if strings.TrimSpace(cfg.ConversionManifestPath) == "" {
		return PlanConfig{}, fmt.Errorf("plan requires -conversion-manifest")
	}
	if strings.TrimSpace(cfg.ReleaseManifestPath) == "" {
		return PlanConfig{}, fmt.Errorf("plan requires -release-manifest")
	}
	if strings.TrimSpace(cfg.DeployPlanPath) == "" {
		return PlanConfig{}, fmt.Errorf("plan requires -deploy-plan")
	}
	if strings.TrimSpace(cfg.Environment) == "" {
		return PlanConfig{}, fmt.Errorf("plan requires -env")
	}
	if cfg.VerifySample < 0 {
		return PlanConfig{}, fmt.Errorf("verify-sample must be >= 0")
	}

	var err error
	cfg.ConversionManifestPath, err = filepath.Abs(cfg.ConversionManifestPath)
	if err != nil {
		return PlanConfig{}, err
	}
	cfg.ReleaseManifestPath, err = filepath.Abs(cfg.ReleaseManifestPath)
	if err != nil {
		return PlanConfig{}, err
	}
	cfg.DeployPlanPath, err = filepath.Abs(cfg.DeployPlanPath)
	if err != nil {
		return PlanConfig{}, err
	}
	pathLabels := []struct {
		flag string
		path string
	}{
		{flag: "-conversion-manifest", path: cfg.ConversionManifestPath},
		{flag: "-release-manifest", path: cfg.ReleaseManifestPath},
		{flag: "-deploy-plan", path: cfg.DeployPlanPath},
	}
	seenPaths := make(map[string]string, len(pathLabels))
	for _, candidate := range pathLabels {
		if previousFlag, exists := seenPaths[candidate.path]; exists {
			return PlanConfig{}, fmt.Errorf("%s and %s must not point to the same file", previousFlag, candidate.flag)
		}
		seenPaths[candidate.path] = candidate.flag
	}
	cfg.OriginRoot, err = normalizeOptionalDir(cfg.OriginRoot)
	if err != nil {
		return PlanConfig{}, err
	}

	cfg.OriginProvider = strings.ToLower(strings.TrimSpace(cfg.OriginProvider))
	cfg.CDNProvider = strings.ToLower(strings.TrimSpace(cfg.CDNProvider))
	if cfg.OriginProvider == "" {
		cfg.OriginProvider = "local"
	}
	if cfg.CDNProvider == "" {
		cfg.CDNProvider = "noop"
	}
	if cfg.OriginProvider != "local" {
		return PlanConfig{}, fmt.Errorf("unsupported origin provider %q", cfg.OriginProvider)
	}
	if cfg.CDNProvider != "noop" {
		return PlanConfig{}, fmt.Errorf("unsupported cdn provider %q", cfg.CDNProvider)
	}
	if cfg.OriginProvider == "local" && strings.TrimSpace(cfg.OriginRoot) == "" {
		return PlanConfig{}, fmt.Errorf("local origin requires -origin-root")
	}

	cfg.OriginPrefix, err = normalizeOptionalObjectPrefix(cfg.OriginPrefix)
	if err != nil {
		return PlanConfig{}, err
	}
	cfg.ImmutablePrefix, err = normalizeRequiredObjectPrefix(cfg.ImmutablePrefix, "immutable-prefix")
	if err != nil {
		return PlanConfig{}, err
	}
	cfg.MutablePrefix, err = normalizeRequiredObjectPrefix(cfg.MutablePrefix, "mutable-prefix")
	if err != nil {
		return PlanConfig{}, err
	}

	if strings.TrimSpace(cfg.BaseURL) != "" {
		parsed, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return PlanConfig{}, fmt.Errorf("invalid base-url: %w", err)
		}
		if !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return PlanConfig{}, fmt.Errorf("invalid base-url: must be an absolute http or https URL")
		}
	}

	return cfg, nil
}

func normalizePublishConfig(cfg PublishConfig) (PublishConfig, error) {
	if strings.TrimSpace(cfg.PlanPath) == "" {
		return PublishConfig{}, fmt.Errorf("publish requires -plan")
	}
	planPath, err := filepath.Abs(cfg.PlanPath)
	if err != nil {
		return PublishConfig{}, err
	}
	cfg.PlanPath = planPath

	switch PublishDryRunMode(strings.ToLower(strings.TrimSpace(string(cfg.DryRunMode)))) {
	case publishDryRunOff:
		cfg.DryRunMode = publishDryRunOff
	case publishDryRunPlan:
		cfg.DryRunMode = publishDryRunPlan
	case publishDryRunVerify:
		cfg.DryRunMode = publishDryRunVerify
	default:
		return PublishConfig{}, fmt.Errorf("unsupported publish dry-run mode %q", cfg.DryRunMode)
	}
	return cfg, nil
}

func normalizeDeliveryVerifyConfig(cfg DeliveryVerifyConfig) (DeliveryVerifyConfig, error) {
	if strings.TrimSpace(cfg.PlanPath) == "" {
		return DeliveryVerifyConfig{}, fmt.Errorf("verify-delivery requires -plan")
	}
	planPath, err := filepath.Abs(cfg.PlanPath)
	if err != nil {
		return DeliveryVerifyConfig{}, err
	}
	cfg.PlanPath = planPath
	return cfg, nil
}

func RunPlan(ctx context.Context, cfg PlanConfig, stdout io.Writer) (PlanSummary, error) {
	if err := ctx.Err(); err != nil {
		return PlanSummary{}, err
	}

	artifactRoot := filepath.Dir(cfg.DeployPlanPath)
	releaseManifestPath, err := resolveContainedArtifactPath(artifactRoot, cfg.ReleaseManifestPath, "release manifest path")
	if err != nil {
		return PlanSummary{}, err
	}
	originRootPath := ""
	if strings.TrimSpace(cfg.OriginRoot) != "" {
		originRootPath, err = resolveContainedArtifactPath(artifactRoot, cfg.OriginRoot, "origin root")
		if err != nil {
			return PlanSummary{}, err
		}
	}
	conversionManifest, err := readConversionManifest(cfg.ConversionManifestPath)
	if err != nil {
		return PlanSummary{}, err
	}

	entries := append([]ManifestEntry(nil), conversionManifest.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RelativePath < entries[j].RelativePath
	})

	immutableKeyPrefix := joinObjectKey(cfg.OriginPrefix, cfg.ImmutablePrefix)
	mutableManifestKey := joinObjectKey(cfg.OriginPrefix, cfg.MutablePrefix, defaultManifestObjectName)

	releaseManifest := ReleaseManifest{
		SchemaVersion: releaseManifestSchemaVersion,
		GeneratedAt:   timeNowRFC3339(),
		Build: ReleaseBuildInfo{
			Encoder: ReleaseEncoderInfo{Name: "cwebp"},
		},
		Assets: []ReleaseAsset{},
		Mutable: ReleaseMutableMetadata{
			ManifestKey:  mutableManifestKey,
			CacheControl: mutableCacheControl,
			ContentType:  contentTypeForExtension("json"),
		},
	}

	uploads := make([]UploadRequest, 0, len(entries)*2)
	verifyCandidates := make([]VerifyCheck, 0, cfg.VerifySample)

	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return PlanSummary{}, err
		}

		sourcePath, outputPath, err := resolveConversionEntryPaths(conversionManifest, entry, "")
		if err != nil {
			return PlanSummary{}, err
		}
		outputVariants := manifestEntryOutputVariants(entry)
		if len(outputVariants) == 0 {
			return PlanSummary{}, fmt.Errorf("conversion manifest entry %q has no output variants", entry.RelativePath)
		}

		logicalPath, err := normalizeLogicalPath(entry.RelativePath)
		if err != nil {
			return PlanSummary{}, err
		}

		sourceExt := strings.TrimPrefix(strings.ToLower(filepath.Ext(logicalPath)), ".")
		if sourceExt == "" {
			return PlanSummary{}, fmt.Errorf("logical path %q has no extension", logicalPath)
		}

		sourceHash, sourceSize, err := hashFileContext(ctx, sourcePath)
		if err != nil {
			return PlanSummary{}, err
		}
		sourceObjectKey, err := immutableObjectKey(immutableKeyPrefix, logicalPath, sourceExt, sourceHash)
		if err != nil {
			return PlanSummary{}, err
		}

		if releaseManifest.Build.Quality == 0 {
			releaseManifest.Build.Quality = entry.Quality
		}
		if releaseManifest.Build.MaxWidth == 0 {
			releaseManifest.Build.MaxWidth = entry.MaxWidth
		}

		asset := ReleaseAsset{
			LogicalID:   logicalPath,
			LogicalPath: logicalPath,
			Source: ReleaseSource{
				SHA256:      sourceHash,
				Bytes:       sourceSize,
				Width:       entry.Width,
				Height:      entry.Height,
				ContentType: contentTypeForExtension(sourceExt),
			},
			Variants: []ReleaseVariant{
				{
					Format:       sourceExt,
					ContentHash:  sourceHash,
					ObjectKey:    sourceObjectKey,
					PublicPath:   publicPathForObjectKey(sourceObjectKey),
					Bytes:        sourceSize,
					Width:        entry.Width,
					Height:       entry.Height,
					ContentType:  contentTypeForExtension(sourceExt),
					CacheControl: immutableCacheControl,
				},
			},
			FallbackFormat:  sourceExt,
			PreferredFormat: "webp",
		}

		stagedSourcePath, err := stageArtifactFile(ctx, sourcePath, artifactRoot, sourceObjectKey, sourceHash)
		if err != nil {
			return PlanSummary{}, err
		}
		stagedSourceRel, err := relativeArtifactPath(artifactRoot, stagedSourcePath)
		if err != nil {
			return PlanSummary{}, err
		}

		uploads = append(uploads, UploadRequest{
			LocalPath:      stagedSourceRel,
			ObjectKey:      sourceObjectKey,
			ContentType:    contentTypeForExtension(sourceExt),
			CacheControl:   immutableCacheControl,
			ContentSHA256:  sourceHash,
			Immutable:      true,
			SkipIfSameHash: true,
		})

		for _, variant := range outputVariants {
			outputPath := outputPath
			if strings.TrimSpace(variant.OutputPath) != "" && variant.OutputPath != entry.OutputPath {
				outputRoot, err := resolveConversionManifestOutputRoot(conversionManifest)
				if err != nil {
					return PlanSummary{}, err
				}
				outputPath, err = resolveContainedArtifactPath(outputRoot, variant.OutputPath, "variant output path")
				if err != nil {
					return PlanSummary{}, err
				}
				if _, err := os.Stat(outputPath); err != nil {
					return PlanSummary{}, fmt.Errorf("conversion manifest output missing for %s (%s): %w", entry.RelativePath, variant.AspectRatio, err)
				}
			}

			outputHash, outputSize, err := hashFileContext(ctx, outputPath)
			if err != nil {
				return PlanSummary{}, err
			}
			webpObjectKey, err := immutableNamedObjectKey(immutableKeyPrefix, logicalPath, "webp", variant.Name, outputHash)
			if err != nil {
				return PlanSummary{}, err
			}
			stagedOutputPath, err := stageArtifactFile(ctx, outputPath, artifactRoot, webpObjectKey, outputHash)
			if err != nil {
				return PlanSummary{}, err
			}
			stagedOutputRel, err := relativeArtifactPath(artifactRoot, stagedOutputPath)
			if err != nil {
				return PlanSummary{}, err
			}

			asset.Variants = append(asset.Variants, ReleaseVariant{
				Name:         variant.Name,
				Usage:        variant.Usage,
				AspectRatio:  variant.AspectRatio,
				Format:       "webp",
				ContentHash:  outputHash,
				ObjectKey:    webpObjectKey,
				PublicPath:   publicPathForObjectKey(webpObjectKey),
				Bytes:        outputSize,
				Width:        variant.OutputWidth,
				Height:       variant.OutputHeight,
				ContentType:  contentTypeForExtension("webp"),
				CacheControl: immutableCacheControl,
			})

			uploads = append(uploads, UploadRequest{
				LocalPath:      stagedOutputRel,
				ObjectKey:      webpObjectKey,
				ContentType:    contentTypeForExtension("webp"),
				CacheControl:   immutableCacheControl,
				ContentSHA256:  outputHash,
				Immutable:      true,
				SkipIfSameHash: true,
			})

			if variant.Usage == variantUsagePrimary && len(verifyCandidates) < cfg.VerifySample {
				verifyCheck := buildVerifyCheck(cfg.BaseURL, webpObjectKey)
				verifyCheck.ExpectStatus = http.StatusOK
				verifyCheck.ExpectContentType = contentTypeForExtension("webp")
				verifyCheck.ExpectSHA256 = outputHash
				verifyCandidates = append(verifyCandidates, verifyCheck)
			}
		}

		releaseManifest.Assets = append(releaseManifest.Assets, asset)
	}

	if err := writeJSONFile(ctx, releaseManifestPath, releaseManifest); err != nil {
		return PlanSummary{}, err
	}

	manifestHash, _, err := hashFileContext(ctx, releaseManifestPath)
	if err != nil {
		return PlanSummary{}, err
	}

	verifyChecks := make([]VerifyCheck, 0, 1+len(verifyCandidates))
	check := buildVerifyCheck(cfg.BaseURL, mutableManifestKey)
	check.ExpectStatus = http.StatusOK
	check.ExpectContentType = contentTypeForExtension("json")
	check.ExpectSHA256 = manifestHash
	if strings.TrimSpace(cfg.BaseURL) != "" {
		check.ExpectCacheControl = mutableCacheControl
	}
	verifyChecks = append(verifyChecks, check)
	verifyChecks = append(verifyChecks, verifyCandidates...)

	releaseManifestRel, err := relativeArtifactPath(artifactRoot, releaseManifestPath)
	if err != nil {
		return PlanSummary{}, err
	}
	conversionManifestRel, err := relativeArtifactPath(artifactRoot, cfg.ConversionManifestPath)
	if err != nil {
		return PlanSummary{}, err
	}
	originRootRel := ""
	if originRootPath != "" {
		originRootRel, err = relativeArtifactPath(artifactRoot, originRootPath)
		if err != nil {
			return PlanSummary{}, err
		}
	}

	deployPlan := DeployPlan{
		SchemaVersion: deployPlanSchemaVersion,
		GeneratedAt:   timeNowRFC3339(),
		Environment:   cfg.Environment,
		Release: DeployPlanRelease{
			ReleaseManifestPath:    releaseManifestRel,
			ConversionManifestPath: conversionManifestRel,
		},
		Origin: OriginTarget{
			Provider: cfg.OriginProvider,
			RootDir:  originRootRel,
			Prefix:   cfg.OriginPrefix,
		},
		CDN: CDNTarget{
			Provider: cfg.CDNProvider,
			BaseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		},
		Uploads: uploads,
		MutableUploads: []UploadRequest{
			{
				LocalPath:      releaseManifestRel,
				ObjectKey:      mutableManifestKey,
				ContentType:    contentTypeForExtension("json"),
				CacheControl:   mutableCacheControl,
				ContentSHA256:  manifestHash,
				Immutable:      false,
				SkipIfSameHash: true,
			},
		},
		Purge: PurgeRequest{
			Enabled: strings.TrimSpace(cfg.BaseURL) != "",
		},
		Verify: DeliveryVerifyPlan{
			Enabled: len(verifyChecks) > 0,
			Checks:  verifyChecks,
		},
	}

	if deployPlan.Purge.Enabled {
		deployPlan.Purge.URLs = []string{joinURL(cfg.BaseURL, mutableManifestKey)}
	}

	if err := writeJSONFile(ctx, cfg.DeployPlanPath, deployPlan); err != nil {
		return PlanSummary{}, err
	}

	if _, err := fmt.Fprintf(stdout, "Planned %d assets for %s\n", len(releaseManifest.Assets), cfg.Environment); err != nil {
		return PlanSummary{}, err
	}
	if _, err := fmt.Fprintf(stdout, "  release manifest: %s\n", releaseManifestPath); err != nil {
		return PlanSummary{}, err
	}
	if _, err := fmt.Fprintf(stdout, "  deploy plan: %s\n", cfg.DeployPlanPath); err != nil {
		return PlanSummary{}, err
	}
	if _, err := fmt.Fprintf(stdout, "  uploads: immutable=%d mutable=%d verify=%d\n", len(deployPlan.Uploads), len(deployPlan.MutableUploads), len(deployPlan.Verify.Checks)); err != nil {
		return PlanSummary{}, err
	}
	return PlanSummary{
		Environment:         cfg.Environment,
		ReleaseManifestPath: releaseManifestPath,
		DeployPlanPath:      cfg.DeployPlanPath,
		Assets:              len(releaseManifest.Assets),
		ImmutableUploads:    len(deployPlan.Uploads),
		MutableUploads:      len(deployPlan.MutableUploads),
		VerifyChecks:        len(deployPlan.Verify.Checks),
	}, nil
}

func RunPublish(ctx context.Context, cfg PublishConfig, stdout io.Writer) (PublishSummary, error) {
	deployPlan, err := readDeployPlan(cfg.PlanPath)
	if err != nil {
		return PublishSummary{}, err
	}
	if err := validateDeployPlan(deployPlan); err != nil {
		return PublishSummary{}, err
	}

	summary := PublishSummary{
		Environment:  deployPlan.Environment,
		VerifyChecks: len(deployPlan.Verify.Checks),
	}

	writef(stdout, "Starting publish for %s (mode=%s)\n", deployPlan.Environment, cfg.DryRunMode)

	if cfg.DryRunMode == publishDryRunPlan {
		printPublishPlan(stdout, deployPlan)
		return summary, nil
	}

	if cfg.DryRunMode == publishDryRunVerify {
		verifySummary, err := runDeliveryVerifyPlan(ctx, deployPlan, stdout)
		if err != nil {
			return summary, err
		}
		summary.VerifyFailures = verifySummary.Failures
		return summary, nil
	}

	originAdapter, err := newOriginAdapter(deployPlan)
	if err != nil {
		return summary, err
	}

	immutableWritten, immutableSkipped, err := executeUploads(ctx, deployPlan.baseDir, originAdapter, deployPlan.Uploads, stdout)
	if err != nil {
		return summary, err
	}
	summary.ImmutableUploads = immutableWritten
	summary.SkippedUploads += immutableSkipped

	mutableWritten, mutableSkipped, err := executeUploads(ctx, deployPlan.baseDir, originAdapter, deployPlan.MutableUploads, stdout)
	if err != nil {
		return summary, err
	}
	summary.MutableUploads = mutableWritten
	summary.SkippedUploads += mutableSkipped

	if deployPlan.Purge.Enabled {
		for _, purgeURL := range deployPlan.Purge.URLs {
			writef(stdout, "[purge] %s\n", purgeURL)
			summary.PurgeRequests++
		}
	}

	verifySummary, err := runDeliveryVerifyPlan(ctx, deployPlan, stdout)
	if err != nil {
		return summary, err
	}
	summary.VerifyFailures = verifySummary.Failures

	writef(stdout, "Publish summary: immutable=%d mutable=%d skipped=%d purge=%d verify=%d failed=%d\n",
		summary.ImmutableUploads,
		summary.MutableUploads,
		summary.SkippedUploads,
		summary.PurgeRequests,
		summary.VerifyChecks,
		summary.VerifyFailures,
	)
	return summary, nil
}

func RunVerifyDelivery(ctx context.Context, cfg DeliveryVerifyConfig, stdout io.Writer) (DeliveryVerifySummary, error) {
	deployPlan, err := readDeployPlan(cfg.PlanPath)
	if err != nil {
		return DeliveryVerifySummary{}, err
	}
	if err := validateDeployPlan(deployPlan); err != nil {
		return DeliveryVerifySummary{}, err
	}
	return runDeliveryVerifyPlan(ctx, deployPlan, stdout)
}

func readConversionManifest(path string) (ConversionManifest, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return ConversionManifest{}, err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ConversionManifest{}, err
	}

	var manifest ConversionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ConversionManifest{}, err
	}
	manifest.manifestPath = absPath
	return manifest, nil
}

func readDeployPlan(path string) (DeployPlan, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return DeployPlan{}, err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return DeployPlan{}, err
	}

	var plan DeployPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return DeployPlan{}, err
	}
	plan.baseDir = filepath.Dir(absPath)
	return plan, nil
}

func validateDeployPlan(plan DeployPlan) error {
	if plan.Verify.Enabled && len(plan.Verify.Checks) == 0 {
		return fmt.Errorf("deploy plan enables verify but has no checks")
	}
	if !plan.Verify.Enabled && len(plan.Verify.Checks) > 0 {
		return fmt.Errorf("deploy plan has verify checks but verify is disabled")
	}
	if plan.Origin.Provider == "local" {
		if strings.TrimSpace(plan.Origin.RootDir) == "" {
			return fmt.Errorf("local origin requires root_dir")
		}
		if plan.baseDir != "" {
			if _, err := resolveContainedArtifactPath(plan.baseDir, plan.Origin.RootDir, "origin root_dir"); err != nil {
				return fmt.Errorf("invalid origin root_dir %q: %w", plan.Origin.RootDir, err)
			}
		}
	}
	for _, req := range append(append([]UploadRequest{}, plan.Uploads...), plan.MutableUploads...) {
		if strings.TrimSpace(req.LocalPath) == "" {
			return fmt.Errorf("deploy plan contains upload with empty local_path")
		}
		if err := validateSHA256Hex("upload content_sha256", req.ContentSHA256); err != nil {
			return fmt.Errorf("invalid upload content_sha256 %q: %w", req.ContentSHA256, err)
		}
		if plan.baseDir != "" {
			if _, err := resolveContainedArtifactPath(plan.baseDir, req.LocalPath, "upload local_path"); err != nil {
				return fmt.Errorf("invalid upload local_path %q: %w", req.LocalPath, err)
			}
		}
		if _, err := normalizeObjectKey(req.ObjectKey); err != nil {
			return fmt.Errorf("invalid object key %q: %w", req.ObjectKey, err)
		}
	}
	for _, check := range plan.Verify.Checks {
		hasObjectKey := strings.TrimSpace(check.ObjectKey) != ""
		hasURL := strings.TrimSpace(check.URL) != ""
		if !hasObjectKey && !hasURL {
			return fmt.Errorf("verify check is missing both url and object_key")
		}
		if hasObjectKey {
			if _, err := normalizeObjectKey(check.ObjectKey); err != nil {
				return fmt.Errorf("invalid verify object key %q: %w", check.ObjectKey, err)
			}
		}
		if hasURL {
			if err := validateVerifyCheckURL(plan, check.URL); err != nil {
				return err
			}
		}
		if strings.TrimSpace(check.ExpectSHA256) != "" {
			if err := validateSHA256Hex("verify expect_sha256", check.ExpectSHA256); err != nil {
				return fmt.Errorf("invalid verify expect_sha256 %q: %w", check.ExpectSHA256, err)
			}
		}
	}
	return nil
}

func executeUploads(ctx context.Context, planBaseDir string, adapter OriginAdapter, uploads []UploadRequest, stdout io.Writer) (int, int, error) {
	written := 0
	skipped := 0

	for _, req := range uploads {
		if err := ctx.Err(); err != nil {
			return written, skipped, err
		}

		resolvedReq, err := resolveUploadRequest(planBaseDir, req)
		if err != nil {
			return written, skipped, err
		}
		uploaded, err := adapter.Upload(ctx, resolvedReq)
		if err != nil {
			return written, skipped, err
		}
		if uploaded {
			if req.Immutable {
				writef(stdout, "[uploaded immutable] %s -> %s\n", req.LocalPath, req.ObjectKey)
			} else {
				writef(stdout, "[uploaded mutable] %s -> %s\n", req.LocalPath, req.ObjectKey)
			}
			written++
			continue
		}

		writef(stdout, "[skipped upload] %s -> %s (same hash)\n", req.LocalPath, req.ObjectKey)
		skipped++
	}

	return written, skipped, nil
}

func printPublishPlan(stdout io.Writer, plan DeployPlan) {
	writef(stdout, "Publish plan for %s\n", plan.Environment)
	writef(stdout, "  origin: %s", plan.Origin.Provider)
	if plan.Origin.RootDir != "" {
		writef(stdout, " (%s)", plan.Origin.RootDir)
	}
	writeLine(stdout)
	writef(stdout, "  cdn: %s\n", plan.CDN.Provider)
	writef(stdout, "  immutable uploads: %d\n", len(plan.Uploads))
	writef(stdout, "  mutable uploads: %d\n", len(plan.MutableUploads))
	writef(stdout, "  purge urls: %d\n", len(plan.Purge.URLs))
	writef(stdout, "  verify checks: %d\n", len(plan.Verify.Checks))

	for _, req := range plan.Uploads {
		writef(stdout, "[plan immutable] %s -> %s\n", req.LocalPath, req.ObjectKey)
	}
	for _, req := range plan.MutableUploads {
		writef(stdout, "[plan mutable] %s -> %s\n", req.LocalPath, req.ObjectKey)
	}
	for _, purgeURL := range plan.Purge.URLs {
		writef(stdout, "[plan purge] %s\n", purgeURL)
	}
	for _, check := range plan.Verify.Checks {
		writef(stdout, "[plan verify] %s\n", describeVerifyTarget(check))
	}
}

func newOriginAdapter(plan DeployPlan) (OriginAdapter, error) {
	switch plan.Origin.Provider {
	case "local":
		if strings.TrimSpace(plan.Origin.RootDir) == "" {
			return nil, fmt.Errorf("local origin requires root_dir")
		}
		rootDir, err := resolveContainedArtifactPath(plan.baseDir, plan.Origin.RootDir, "origin root_dir")
		if err != nil {
			return nil, err
		}
		return &LocalOriginAdapter{RootDir: rootDir}, nil
	default:
		return nil, fmt.Errorf("unsupported origin provider %q", plan.Origin.Provider)
	}
}

func (a *LocalOriginAdapter) Stat(ctx context.Context, key string) (ObjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return ObjectMeta{}, err
	}

	targetPath, err := a.targetPath(key)
	if err != nil {
		return ObjectMeta{}, err
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		return ObjectMeta{}, err
	}
	hash, _, err := hashFileContext(ctx, targetPath)
	if err != nil {
		return ObjectMeta{}, err
	}
	return ObjectMeta{
		Key:    key,
		Size:   info.Size(),
		SHA256: hash,
	}, nil
}

func (a *LocalOriginAdapter) Upload(ctx context.Context, req UploadRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	targetPath, err := a.targetPath(req.ObjectKey)
	if err != nil {
		return false, err
	}
	if err := validateSHA256Hex("upload content_sha256", req.ContentSHA256); err != nil {
		return false, err
	}
	sourceHash, _, err := hashFileContext(ctx, req.LocalPath)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(sourceHash, req.ContentSHA256) {
		return false, fmt.Errorf("upload local_path %q hash mismatch: expected %s got %s", req.LocalPath, req.ContentSHA256, sourceHash)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return false, err
	}

	if req.SkipIfSameHash {
		meta, err := a.Stat(ctx, req.ObjectKey)
		if err == nil && meta.SHA256 == req.ContentSHA256 {
			return false, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}

	source, err := os.Open(req.LocalPath)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = source.Close()
	}()

	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), ".webp-guard-publish-*")
	if err != nil {
		return false, err
	}

	cleanup := func() {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
	}
	defer cleanup()

	if _, err := copyWithContext(ctx, tempFile, source); err != nil {
		return false, err
	}
	if err := tempFile.Close(); err != nil {
		return false, err
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := os.Rename(tempFile.Name(), targetPath); err != nil {
		return false, err
	}
	return true, nil
}

func (a *LocalOriginAdapter) targetPath(key string) (string, error) {
	cleanKey, err := normalizeObjectKey(key)
	if err != nil {
		return "", err
	}

	return resolveContainedArtifactPath(a.RootDir, filepath.FromSlash(cleanKey), "object key")
}

func runDeliveryVerifyPlan(ctx context.Context, deployPlan DeployPlan, stdout io.Writer) (DeliveryVerifySummary, error) {
	summary := DeliveryVerifySummary{Total: len(deployPlan.Verify.Checks)}
	if !deployPlan.Verify.Enabled {
		if len(deployPlan.Verify.Checks) > 0 {
			return summary, fmt.Errorf("delivery verify is disabled but %d checks are present", len(deployPlan.Verify.Checks))
		}
		writeLine(stdout, "Delivery verify is disabled in this plan")
		return summary, nil
	}

	client := newDeliveryVerifyHTTPClient(deployPlan)

	for _, check := range deployPlan.Verify.Checks {
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		ok, reason, err := executeVerifyCheck(ctx, client, deployPlan, check)
		if err != nil {
			return summary, err
		}
		if ok {
			writef(stdout, "[verify ok] %s\n", describeVerifyTarget(check))
			summary.Verified++
			continue
		}
		writef(stdout, "[verify failed] %s (%s)\n", describeVerifyTarget(check), reason)
		summary.Failures++
	}

	writef(stdout, "Delivery verify summary: total=%d verified=%d failed=%d\n", summary.Total, summary.Verified, summary.Failures)
	return summary, nil
}

func executeVerifyCheck(ctx context.Context, client *http.Client, plan DeployPlan, check VerifyCheck) (bool, string, error) {
	targetURL, err := resolveVerifyCheckURL(plan, check)
	if err != nil {
		return false, "", err
	}
	parsed, err := url.Parse(targetURL)
	if err != nil {
		return false, "", err
	}

	switch parsed.Scheme {
	case "file":
		return verifyFileURL(ctx, parsed, check)
	case "http", "https":
		return verifyHTTPURL(ctx, client, targetURL, check)
	default:
		return false, fmt.Sprintf("unsupported verify scheme %q", parsed.Scheme), nil
	}
}

func verifyFileURL(ctx context.Context, parsed *url.URL, check VerifyCheck) (bool, string, error) {
	if err := ctx.Err(); err != nil {
		return false, "", err
	}

	filePath := localPathFromFileURL(parsed)
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, "missing file", nil
		}
		return false, "", err
	}
	if !info.Mode().IsRegular() {
		return false, "not a regular file", nil
	}
	if check.ExpectStatus != 0 && check.ExpectStatus != http.StatusOK {
		return false, fmt.Sprintf("expected status %d but file URLs can only satisfy 200", check.ExpectStatus), nil
	}

	actualType := contentTypeForExtension(strings.TrimPrefix(filepath.Ext(filePath), "."))
	if check.ExpectContentType != "" && !contentTypeMatches(actualType, check.ExpectContentType) {
		return false, fmt.Sprintf("content-type expected %s got %s", check.ExpectContentType, actualType), nil
	}
	if check.ExpectCacheControl != "" {
		return false, "cache-control is not available for file URLs", nil
	}
	if check.ExpectSHA256 != "" {
		hash, _, err := hashFileContext(ctx, filePath)
		if err != nil {
			return false, "", err
		}
		if !strings.EqualFold(hash, check.ExpectSHA256) {
			return false, fmt.Sprintf("sha256 expected %s got %s", check.ExpectSHA256, hash), nil
		}
	}

	return true, "", nil
}

func localPathFromFileURL(parsed *url.URL) string {
	path := parsed.Path
	switch {
	case parsed.Host == "" || strings.EqualFold(parsed.Host, "localhost"):
		if hasWindowsDrivePath(path) {
			path = strings.TrimPrefix(path, "/")
		}
		return filepath.FromSlash(path)
	case isWindowsDriveVolume(parsed.Host):
		return filepath.FromSlash(parsed.Host + path)
	default:
		return filepath.FromSlash("//" + parsed.Host + path)
	}
}

func isWindowsDriveVolume(value string) bool {
	if len(value) != 2 || value[1] != ':' {
		return false
	}
	letter := value[0]
	return (letter >= 'A' && letter <= 'Z') || (letter >= 'a' && letter <= 'z')
}

func hasWindowsDrivePath(path string) bool {
	return len(path) >= 3 && path[0] == '/' && isWindowsDriveVolume(path[1:3])
}

func newDeliveryVerifyHTTPClient(plan DeployPlan) *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after %d redirects", len(via))
			}
			return validateVerifyCheckURL(plan, req.URL.String())
		},
	}
}

func verifyHTTPURL(ctx context.Context, client *http.Client, targetURL string, check VerifyCheck) (bool, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return false, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if check.ExpectStatus != 0 && resp.StatusCode != check.ExpectStatus {
		return false, fmt.Sprintf("status expected %d got %d", check.ExpectStatus, resp.StatusCode), nil
	}

	if check.ExpectContentType != "" && !contentTypeMatches(resp.Header.Get("Content-Type"), check.ExpectContentType) {
		return false, fmt.Sprintf("content-type expected %s got %s", check.ExpectContentType, resp.Header.Get("Content-Type")), nil
	}
	if check.ExpectCacheControl != "" && strings.TrimSpace(resp.Header.Get("Cache-Control")) != check.ExpectCacheControl {
		return false, fmt.Sprintf("cache-control expected %s got %s", check.ExpectCacheControl, resp.Header.Get("Cache-Control")), nil
	}

	if check.ExpectSHA256 != "" {
		hash, err := hashReaderContext(ctx, resp.Body)
		if err != nil {
			return false, "", err
		}
		if !strings.EqualFold(hash, check.ExpectSHA256) {
			return false, fmt.Sprintf("sha256 expected %s got %s", check.ExpectSHA256, hash), nil
		}
		return true, "", nil
	}

	if _, err := copyWithContext(ctx, io.Discard, resp.Body); err != nil {
		return false, "", err
	}
	return true, "", nil
}

func resolveConversionEntryPaths(manifest ConversionManifest, entry ManifestEntry, sourceRootOverride string) (string, string, error) {
	sourceRoot, err := resolveConversionManifestSourceRoot(manifest, sourceRootOverride)
	if err != nil {
		return "", "", err
	}
	outputRoot, err := resolveConversionManifestOutputRoot(manifest)
	if err != nil {
		return "", "", err
	}

	sourcePath, err := resolveContainedArtifactPath(sourceRoot, entry.SourcePath, "source path")
	if err != nil {
		return "", "", err
	}
	outputPath, err := resolveContainedArtifactPath(outputRoot, entry.OutputPath, "output path")
	if err != nil {
		return "", "", err
	}

	if _, err := os.Stat(sourcePath); err != nil {
		return "", "", fmt.Errorf("conversion manifest source missing for %s: %w", entry.RelativePath, err)
	}
	if _, err := os.Stat(outputPath); err != nil {
		return "", "", fmt.Errorf("conversion manifest output missing for %s: %w", entry.RelativePath, err)
	}
	return sourcePath, outputPath, nil
}

func normalizeLogicalPath(value string) (string, error) {
	cleaned := filepath.ToSlash(strings.TrimSpace(value))
	if cleaned == "" {
		return "", fmt.Errorf("relative_path cannot be empty")
	}
	if strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("relative_path %q must not be absolute", value)
	}
	if strings.HasPrefix(pathpkg.Clean(cleaned), "../") || pathpkg.Clean(cleaned) == ".." {
		return "", fmt.Errorf("relative_path %q escapes the root", value)
	}
	return pathpkg.Clean(cleaned), nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		_ = file.Close()
	}()

	hash, size, err := hashStream(file)
	if err != nil {
		return "", 0, err
	}
	return hash, size, nil
}

func hashFileContext(ctx context.Context, path string) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}

	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		_ = file.Close()
	}()

	hash, size, err := hashStreamContext(ctx, file)
	if err != nil {
		return "", 0, err
	}
	return hash, size, nil
}

func hashReader(reader io.Reader) (string, error) {
	hash, _, err := hashStream(reader)
	if err != nil {
		return "", err
	}
	return hash, nil
}

func hashReaderContext(ctx context.Context, reader io.Reader) (string, error) {
	hash, _, err := hashStreamContext(ctx, reader)
	if err != nil {
		return "", err
	}
	return hash, nil
}

func hashStream(reader io.Reader) (string, int64, error) {
	hasher := sha256.New()
	written, err := io.Copy(hasher, reader)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

func hashStreamContext(ctx context.Context, reader io.Reader) (string, int64, error) {
	hasher := sha256.New()
	written, err := copyWithContext(ctx, hasher, reader)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	buf := make([]byte, 32*1024)
	var written int64

	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}

		readBytes, readErr := src.Read(buf)
		if readBytes > 0 {
			writtenBytes, writeErr := dst.Write(buf[:readBytes])
			written += int64(writtenBytes)
			if writeErr != nil {
				return written, writeErr
			}
			if writtenBytes != readBytes {
				return written, io.ErrShortWrite
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				return written, nil
			}
			return written, readErr
		}
	}
}

func immutableObjectKey(prefix string, logicalPath string, format string, contentHash string) (string, error) {
	return immutableNamedObjectKey(prefix, logicalPath, format, "", contentHash)
}

func immutableNamedObjectKey(prefix string, logicalPath string, format string, name string, contentHash string) (string, error) {
	cleanLogicalPath, err := normalizeLogicalPath(logicalPath)
	if err != nil {
		return "", err
	}
	base := strings.TrimSuffix(pathpkg.Base(cleanLogicalPath), pathpkg.Ext(cleanLogicalPath))
	dir := pathpkg.Dir(cleanLogicalPath)
	if strings.TrimSpace(name) != "" {
		base = base + "." + name
	}
	fileName := fmt.Sprintf("%s.%s.%s", base, shortenHash(contentHash), format)

	if dir == "." {
		return normalizeObjectKey(joinObjectKey(prefix, fileName))
	}
	return normalizeObjectKey(joinObjectKey(prefix, dir, fileName))
}

func publicPathForObjectKey(objectKey string) string {
	return "/" + strings.TrimLeft(objectKey, "/")
}

func joinObjectKey(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.Trim(part, "/")
		if trimmed == "" {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	return strings.Join(filtered, "/")
}

func normalizeObjectKey(value string) (string, error) {
	cleaned := pathpkg.Clean(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("object key cannot be empty")
	}
	if strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("object key %q escapes the target prefix", value)
	}
	return cleaned, nil
}

func normalizeOptionalObjectPrefix(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	return normalizeObjectKey(value)
}

func normalizeRequiredObjectPrefix(value string, flagName string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s cannot be empty", flagName)
	}
	return normalizeObjectKey(value)
}

func shortenHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func buildVerifyCheck(baseURL string, objectKey string) VerifyCheck {
	if strings.TrimSpace(baseURL) != "" {
		return VerifyCheck{URL: joinURL(baseURL, objectKey)}
	}
	return VerifyCheck{ObjectKey: objectKey}
}

func joinURL(baseURL string, objectKey string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(objectKey, "/")
	}
	joinedPath := strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(objectKey, "/")
	if joinedPath == "" {
		joinedPath = "/"
	}
	parsed.Path = joinedPath
	parsed.RawPath = ""
	return parsed.String()
}

func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func writeJSONFile(ctx context.Context, path string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".webp-guard-json-*")
	if err != nil {
		return err
	}

	cleanup := func() {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
	}
	defer cleanup()

	encoder := json.NewEncoder(tempFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(tempFile.Name(), path)
}

func contentTypeForExtension(ext string) string {
	switch strings.TrimPrefix(strings.ToLower(ext), ".") {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "json":
		return "application/json"
	default:
		if guessed := mime.TypeByExtension("." + strings.TrimPrefix(strings.ToLower(ext), ".")); guessed != "" {
			return guessed
		}
		return "application/octet-stream"
	}
}

func contentTypeMatches(actual string, expected string) bool {
	actualType, _, err := mime.ParseMediaType(actual)
	if err != nil {
		actualType = strings.TrimSpace(actual)
	}
	expectedType, _, err := mime.ParseMediaType(expected)
	if err != nil {
		expectedType = strings.TrimSpace(expected)
	}
	return strings.EqualFold(actualType, expectedType)
}

func relativeArtifactPath(baseDir string, target string) (string, error) {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	relativePath, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(relativePath), nil
}

func validateVerifyCheckURL(plan DeployPlan, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid verify url %q: %w", rawURL, err)
	}
	if !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid verify url %q: must be an absolute http or https URL", rawURL)
	}
	if strings.TrimSpace(plan.CDN.BaseURL) == "" {
		return fmt.Errorf("verify url %q is not allowed when cdn.base_url is empty", rawURL)
	}

	baseURL, err := url.Parse(plan.CDN.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid plan cdn.base_url %q: %w", plan.CDN.BaseURL, err)
	}
	if !strings.EqualFold(parsed.Scheme, baseURL.Scheme) || !strings.EqualFold(parsed.Host, baseURL.Host) {
		return fmt.Errorf("verify url %q must stay under cdn.base_url %q", rawURL, plan.CDN.BaseURL)
	}
	if !urlPathWithinBase(baseURL, parsed) {
		return fmt.Errorf("verify url %q must stay under cdn.base_url %q", rawURL, plan.CDN.BaseURL)
	}
	return nil
}

func validateSHA256Hex(label string, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s cannot be empty", label)
	}
	if len(trimmed) != sha256.Size*2 {
		return fmt.Errorf("%s must be %d hex characters", label, sha256.Size*2)
	}
	if _, err := hex.DecodeString(trimmed); err != nil {
		return fmt.Errorf("%s must be valid hex: %w", label, err)
	}
	return nil
}

func urlPathWithinBase(baseURL *url.URL, targetURL *url.URL) bool {
	basePath := strings.TrimSuffix(pathpkg.Clean(baseURL.Path), "/")
	if basePath == "." {
		basePath = ""
	}
	targetPath := pathpkg.Clean(targetURL.Path)
	if basePath == "" || basePath == "/" {
		return strings.HasPrefix(targetPath, "/")
	}
	return targetPath == basePath || strings.HasPrefix(targetPath, basePath+"/")
}

func resolveArtifactPath(baseDir string, target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("artifact path cannot be empty")
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), nil
	}
	return filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(target))), nil
}

func resolveContainedArtifactPath(root string, target string, label string) (string, error) {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := resolvePathWithExistingSymlinks(rootPath)
	if err != nil {
		return "", err
	}

	targetPath, err := resolveArtifactPath(rootPath, target)
	if err != nil {
		return "", err
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return "", err
	}
	resolvedPath, err := resolvePathWithExistingSymlinks(targetPath)
	if err != nil {
		return "", err
	}

	insideRoot, err := pathWithinRoot(resolvedRoot, resolvedPath)
	if err != nil {
		return "", err
	}
	if !insideRoot {
		return "", fmt.Errorf("%s %q escapes root %q", label, target, rootPath)
	}
	return targetPath, nil
}

func resolvePathWithExistingSymlinks(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absPath = filepath.Clean(absPath)

	resolvedPrefix := absPath
	missingParts := make([]string, 0, 4)
	for {
		_, err := os.Lstat(resolvedPrefix)
		if err == nil {
			resolvedPrefix, err = filepath.EvalSymlinks(resolvedPrefix)
			if err != nil {
				return "", err
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(resolvedPrefix)
		if parent == resolvedPrefix {
			break
		}
		missingParts = append(missingParts, filepath.Base(resolvedPrefix))
		resolvedPrefix = parent
	}

	for i := len(missingParts) - 1; i >= 0; i-- {
		resolvedPrefix = filepath.Join(resolvedPrefix, missingParts[i])
	}
	return filepath.Clean(resolvedPrefix), nil
}

func resolveConversionManifestSourceRoot(manifest ConversionManifest, override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return filepath.Clean(override), nil
	}
	return resolveConversionManifestRoot(manifest.manifestPath, manifest.RootDir)
}

func resolveConversionManifestOutputRoot(manifest ConversionManifest) (string, error) {
	if strings.TrimSpace(manifest.OutputDir) == "" {
		return resolveConversionManifestRoot(manifest.manifestPath, manifest.RootDir)
	}
	return resolveConversionManifestRoot(manifest.manifestPath, manifest.OutputDir)
}

func resolveConversionManifestRoot(manifestPath string, root string) (string, error) {
	manifestDir := filepath.Dir(manifestPath)
	if strings.TrimSpace(root) == "" {
		return manifestDir, nil
	}
	return resolveArtifactPath(manifestDir, root)
}

func stageArtifactFile(ctx context.Context, sourcePath string, artifactRoot string, objectKey string, expectedHash string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	targetPath := filepath.Join(artifactRoot, filepath.FromSlash(objectKey))
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", err
	}

	if currentHash, _, err := hashFileContext(ctx, targetPath); err == nil && strings.EqualFold(currentHash, expectedHash) {
		return targetPath, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = source.Close()
	}()

	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), ".webp-guard-stage-*")
	if err != nil {
		return "", err
	}
	cleanup := func() {
		_ = tempFile.Close()
		_ = os.Remove(tempFile.Name())
	}
	defer cleanup()

	if _, err := copyWithContext(ctx, tempFile, source); err != nil {
		return "", err
	}
	if err := tempFile.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Rename(tempFile.Name(), targetPath); err != nil {
		return "", err
	}
	return targetPath, nil
}

func resolveUploadRequest(planBaseDir string, req UploadRequest) (UploadRequest, error) {
	resolvedPath, err := resolveContainedArtifactPath(planBaseDir, req.LocalPath, "upload local_path")
	if err != nil {
		return UploadRequest{}, err
	}
	req.LocalPath = resolvedPath
	return req, nil
}

func resolvePlanPath(planBaseDir string, path string) (string, error) {
	return resolveArtifactPath(planBaseDir, path)
}

func resolveVerifyCheckURL(plan DeployPlan, check VerifyCheck) (string, error) {
	if strings.TrimSpace(check.URL) != "" {
		return check.URL, nil
	}
	if strings.TrimSpace(check.ObjectKey) == "" {
		return "", fmt.Errorf("verify check is missing both url and object_key")
	}
	if plan.Origin.Provider != "local" {
		return "", fmt.Errorf("verify check object_key requires local origin, got %q", plan.Origin.Provider)
	}
	originRoot, err := resolvePlanPath(plan.baseDir, plan.Origin.RootDir)
	if err != nil {
		return "", err
	}
	cleanKey, err := normalizeObjectKey(check.ObjectKey)
	if err != nil {
		return "", fmt.Errorf("invalid verify object key %q: %w", check.ObjectKey, err)
	}
	resolvedPath, err := resolveContainedArtifactPath(originRoot, filepath.FromSlash(cleanKey), "verify object key")
	if err != nil {
		return "", err
	}
	return fileURL(resolvedPath), nil
}

func describeVerifyTarget(check VerifyCheck) string {
	if check.URL != "" {
		return check.URL
	}
	return check.ObjectKey
}
