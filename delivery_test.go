package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunProcessCommandUsesOutDirAndSkipsNestedOutputTree(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, ".generated")
	sourceDir := filepath.Join(root, "images")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(sourceDir, "hero.jpg")
	writeJPEG(t, source, 1600, 800)
	stale := filepath.Join(outDir, "stale.png")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(root)
	cfg.OutDir = outDir
	cfg.ManifestPath = filepath.Join(t.TempDir(), "conversion-manifest.json")

	var stdout bytes.Buffer
	summary, err := RunProcessCommand(context.Background(), cfg, newDimensionAwareFakeEncoder(t, 64, nil), &stdout)
	if err != nil {
		t.Fatal(err)
	}

	if summary.Total != 1 || summary.Converted != 1 {
		t.Fatalf("expected exactly one converted image, got %#v", summary)
	}

	outputPath := filepath.Join(outDir, "images", "hero.webp")
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected output in out-dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "hero.webp")); !os.IsNotExist(err) {
		t.Fatalf("did not expect adjacent output, got err=%v", err)
	}

	manifestBytes, err := os.ReadFile(cfg.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifestBytes), filepath.ToSlash(root)) {
		t.Fatalf("conversion manifest should not contain absolute source root")
	}
	if strings.Contains(string(manifestBytes), filepath.ToSlash(outDir)) {
		t.Fatalf("conversion manifest should not contain absolute output root")
	}
}

func TestLocalPathFromFileURLPreservesWindowsDriveHost(t *testing.T) {
	parsed, err := url.Parse("file://C:/artifacts/release-manifest.json")
	if err != nil {
		t.Fatal(err)
	}

	got := localPathFromFileURL(parsed)
	want := filepath.FromSlash("C:/artifacts/release-manifest.json")
	if got != want {
		t.Fatalf("expected Windows drive to be preserved, got %q want %q", got, want)
	}
}

func TestLocalPathFromFileURLPreservesWindowsDrivePath(t *testing.T) {
	parsed, err := url.Parse("file:///C:/artifacts/release-manifest.json")
	if err != nil {
		t.Fatal(err)
	}

	got := localPathFromFileURL(parsed)
	want := filepath.FromSlash("C:/artifacts/release-manifest.json")
	if got != want {
		t.Fatalf("expected Windows drive to be preserved, got %q want %q", got, want)
	}
}

func TestPlanPublishAndVerifyDeliveryLocalE2E(t *testing.T) {
	root := t.TempDir()
	artifactDir := filepath.Join(t.TempDir(), "artifact")
	outDir := filepath.Join(artifactDir, "conversion-assets")
	originRoot := filepath.Join(artifactDir, "dev-origin")

	source := filepath.Join(root, "images", "hero.jpg")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, source, 1600, 800)

	conversionManifest := filepath.Join(artifactDir, "conversion-manifest.json")
	cfg := testConfig(root)
	cfg.OutDir = outDir
	cfg.ManifestPath = conversionManifest

	var bulkStdout bytes.Buffer
	summary, err := RunProcessCommand(context.Background(), cfg, newDimensionAwareFakeEncoder(t, 64, nil), &bulkStdout)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Converted != 1 {
		t.Fatalf("expected one converted file, got %#v", summary)
	}

	releaseManifest := filepath.Join(artifactDir, "release-manifest.json")
	deployPlanPath := filepath.Join(artifactDir, "deploy-plan.dev.json")

	var planStdout bytes.Buffer
	if _, err := RunPlan(context.Background(), PlanConfig{
		ConversionManifestPath: conversionManifest,
		ReleaseManifestPath:    releaseManifest,
		DeployPlanPath:         deployPlanPath,
		Environment:            "dev",
		OriginProvider:         "local",
		OriginRoot:             originRoot,
		CDNProvider:            "noop",
		ImmutablePrefix:        "assets",
		MutablePrefix:          "release",
		VerifySample:           2,
	}, &planStdout); err != nil {
		t.Fatal(err)
	}

	releaseManifestBytes, err := os.ReadFile(releaseManifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(releaseManifestBytes), filepath.ToSlash(root)) {
		t.Fatalf("public release manifest leaked source root")
	}
	if strings.Contains(string(releaseManifestBytes), filepath.ToSlash(outDir)) {
		t.Fatalf("public release manifest leaked output root")
	}

	var release ReleaseManifest
	if err := json.Unmarshal(releaseManifestBytes, &release); err != nil {
		t.Fatal(err)
	}
	if len(release.Assets) != 1 {
		t.Fatalf("expected one release asset, got %d", len(release.Assets))
	}
	if len(release.Assets[0].Variants) != 2 {
		t.Fatalf("expected original + webp variants, got %#v", release.Assets[0].Variants)
	}

	var deployPlan DeployPlan
	deployPlanBytes, err := os.ReadFile(deployPlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(deployPlanBytes, &deployPlan); err != nil {
		t.Fatal(err)
	}
	if len(deployPlan.Uploads) != 2 {
		t.Fatalf("expected two immutable uploads, got %d", len(deployPlan.Uploads))
	}
	if len(deployPlan.MutableUploads) != 1 {
		t.Fatalf("expected one mutable upload, got %d", len(deployPlan.MutableUploads))
	}
	if filepath.IsAbs(deployPlan.Release.ReleaseManifestPath) || filepath.IsAbs(deployPlan.Release.ConversionManifestPath) {
		t.Fatalf("deploy plan should keep artifact-relative manifest paths: %#v", deployPlan.Release)
	}
	for _, upload := range append(append([]UploadRequest{}, deployPlan.Uploads...), deployPlan.MutableUploads...) {
		if filepath.IsAbs(upload.LocalPath) {
			t.Fatalf("deploy plan should keep artifact-relative upload paths: %#v", upload)
		}
	}
	for _, check := range deployPlan.Verify.Checks {
		if check.URL != "" && strings.HasPrefix(check.URL, "file:///") {
			t.Fatalf("verify checks should not bake absolute file URLs: %#v", check)
		}
	}

	relocatedParent := t.TempDir()
	relocatedArtifact := filepath.Join(relocatedParent, "relocated-artifact")
	if err := os.Rename(artifactDir, relocatedArtifact); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	deployPlanPath = filepath.Join(relocatedArtifact, "deploy-plan.dev.json")

	var publishPlanStdout bytes.Buffer
	planSummary, err := RunPublish(context.Background(), PublishConfig{
		PlanPath:   deployPlanPath,
		DryRunMode: publishDryRunPlan,
	}, &publishPlanStdout)
	if err != nil {
		t.Fatal(err)
	}
	if planSummary.VerifyFailures != 0 {
		t.Fatalf("did not expect dry-run plan failures, got %#v", planSummary)
	}
	if !strings.Contains(publishPlanStdout.String(), "[plan mutable]") {
		t.Fatalf("expected human-readable publish plan output, got %q", publishPlanStdout.String())
	}

	var publishStdout bytes.Buffer
	publishSummary, err := RunPublish(context.Background(), PublishConfig{
		PlanPath:   deployPlanPath,
		DryRunMode: publishDryRunOff,
	}, &publishStdout)
	if err != nil {
		t.Fatal(err)
	}
	if publishSummary.ImmutableUploads != 2 || publishSummary.MutableUploads != 1 {
		t.Fatalf("unexpected publish summary: %#v", publishSummary)
	}
	if publishSummary.VerifyFailures != 0 {
		t.Fatalf("publish verify should succeed: %#v", publishSummary)
	}

	deployPlanBytes, err = os.ReadFile(deployPlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(deployPlanBytes, &deployPlan); err != nil {
		t.Fatal(err)
	}

	for _, upload := range append(append([]UploadRequest{}, deployPlan.Uploads...), deployPlan.MutableUploads...) {
		target := filepath.Join(relocatedArtifact, "dev-origin", filepath.FromSlash(upload.ObjectKey))
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("expected published file %s: %v", target, err)
		}
	}

	var verifyStdout bytes.Buffer
	verifySummary, err := RunVerifyDelivery(context.Background(), DeliveryVerifyConfig{
		PlanPath: deployPlanPath,
	}, &verifyStdout)
	if err != nil {
		t.Fatal(err)
	}
	if verifySummary.Failures != 0 {
		t.Fatalf("verify-delivery should succeed, got %#v\n%s", verifySummary, verifyStdout.String())
	}
}

func TestPlanIncludesAspectVariants(t *testing.T) {
	root := t.TempDir()
	artifactDir := filepath.Join(t.TempDir(), "artifact")
	outDir := filepath.Join(artifactDir, "conversion-assets")
	originRoot := filepath.Join(artifactDir, "dev-origin")

	source := filepath.Join(root, "images", "hero.jpg")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, source, 2400, 1200)

	aspectVariants, err := parseAspectVariants("16:9,4:3,1:1")
	if err != nil {
		t.Fatal(err)
	}

	conversionManifest := filepath.Join(artifactDir, "conversion-manifest.json")
	cfg := testConfig(root)
	cfg.OutDir = outDir
	cfg.ManifestPath = conversionManifest
	cfg.AspectVariants = aspectVariants

	var bulkStdout bytes.Buffer
	summary, err := RunProcessCommand(context.Background(), cfg, newDimensionAwareFakeEncoder(t, 64, nil), &bulkStdout)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Converted != 1 {
		t.Fatalf("expected one converted file, got %#v", summary)
	}

	releaseManifest := filepath.Join(artifactDir, "release-manifest.json")
	deployPlanPath := filepath.Join(artifactDir, "deploy-plan.dev.json")
	if _, err := RunPlan(context.Background(), PlanConfig{
		ConversionManifestPath: conversionManifest,
		ReleaseManifestPath:    releaseManifest,
		DeployPlanPath:         deployPlanPath,
		Environment:            "dev",
		OriginProvider:         "local",
		OriginRoot:             originRoot,
		CDNProvider:            "noop",
		ImmutablePrefix:        "assets",
		MutablePrefix:          "release",
		VerifySample:           2,
	}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	releaseManifestBytes, err := os.ReadFile(releaseManifest)
	if err != nil {
		t.Fatal(err)
	}

	var release ReleaseManifest
	if err := json.Unmarshal(releaseManifestBytes, &release); err != nil {
		t.Fatal(err)
	}
	if len(release.Assets) != 1 {
		t.Fatalf("expected one release asset, got %d", len(release.Assets))
	}
	if len(release.Assets[0].Variants) != 4 {
		t.Fatalf("expected original + 3 webp variants, got %#v", release.Assets[0].Variants)
	}

	webpVariants := release.Assets[0].Variants[1:]
	if webpVariants[0].Usage != variantUsagePrimary || webpVariants[0].AspectRatio != "16:9" {
		t.Fatalf("expected first webp variant to be primary 16:9, got %#v", webpVariants[0])
	}
	if webpVariants[1].AspectRatio != "4:3" || webpVariants[2].AspectRatio != "1:1" {
		t.Fatalf("expected supporting aspect ratios 4:3 and 1:1, got %#v", webpVariants)
	}

	deployPlanBytes, err := os.ReadFile(deployPlanPath)
	if err != nil {
		t.Fatal(err)
	}
	var deployPlan DeployPlan
	if err := json.Unmarshal(deployPlanBytes, &deployPlan); err != nil {
		t.Fatal(err)
	}
	if len(deployPlan.Uploads) != 4 {
		t.Fatalf("expected source + 3 webp uploads, got %d", len(deployPlan.Uploads))
	}
	if len(deployPlan.Verify.Checks) != 2 {
		t.Fatalf("expected release manifest + primary verify checks, got %d", len(deployPlan.Verify.Checks))
	}
	if !strings.Contains(deployPlan.Uploads[1].ObjectKey, ".16x9.") {
		t.Fatalf("expected primary webp object key to include aspect name, got %s", deployPlan.Uploads[1].ObjectKey)
	}
}

func TestNormalizePlanConfigRequiresAbsoluteHTTPBaseURL(t *testing.T) {
	root := t.TempDir()
	baseCfg := PlanConfig{
		ConversionManifestPath: filepath.Join(root, "conversion-manifest.json"),
		ReleaseManifestPath:    filepath.Join(root, "release-manifest.json"),
		DeployPlanPath:         filepath.Join(root, "deploy-plan.dev.json"),
		Environment:            "dev",
		OriginProvider:         "local",
		OriginRoot:             root,
		CDNProvider:            "noop",
		ImmutablePrefix:        "assets",
		MutablePrefix:          "release",
	}

	_, err := normalizePlanConfig(baseCfg)
	if err != nil {
		t.Fatal(err)
	}

	_, err = normalizePlanConfig(func() PlanConfig {
		cfg := baseCfg
		cfg.BaseURL = "cdn.example.com/assets"
		return cfg
	}())
	if err == nil {
		t.Fatal("expected scheme-less base-url to fail")
	}
	if !strings.Contains(err.Error(), "absolute http or https URL") {
		t.Fatalf("unexpected error for scheme-less base-url: %v", err)
	}

	_, err = normalizePlanConfig(func() PlanConfig {
		cfg := baseCfg
		cfg.BaseURL = "ftp://cdn.example.com/assets"
		return cfg
	}())
	if err == nil {
		t.Fatal("expected non-http base-url to fail")
	}
	if !strings.Contains(err.Error(), "absolute http or https URL") {
		t.Fatalf("unexpected error for ftp base-url: %v", err)
	}

	normalized, err := normalizePlanConfig(func() PlanConfig {
		cfg := baseCfg
		cfg.BaseURL = "https://cdn.example.com/assets"
		return cfg
	}())
	if err != nil {
		t.Fatal(err)
	}
	if normalized.BaseURL != "https://cdn.example.com/assets" {
		t.Fatalf("expected base-url to be preserved, got %q", normalized.BaseURL)
	}
}

func TestNormalizePlanConfigRejectsCollidingArtifactPaths(t *testing.T) {
	root := t.TempDir()
	sharedPath := filepath.Join(root, "artifacts.json")

	_, err := normalizePlanConfig(PlanConfig{
		ConversionManifestPath: filepath.Join(root, "conversion-manifest.json"),
		ReleaseManifestPath:    sharedPath,
		DeployPlanPath:         sharedPath,
		Environment:            "dev",
		OriginProvider:         "local",
		OriginRoot:             root,
		CDNProvider:            "noop",
		ImmutablePrefix:        "assets",
		MutablePrefix:          "release",
	})
	if err == nil {
		t.Fatal("expected colliding release and deploy outputs to fail")
	}
	if !strings.Contains(err.Error(), "-release-manifest and -deploy-plan must not point to the same file") {
		t.Fatalf("unexpected collision error: %v", err)
	}
}

func TestJoinURLPathEscapesReservedCharacters(t *testing.T) {
	joined := joinURL("https://cdn.example.com/assets", "nested/hero?#.webp")
	parsed, err := url.Parse(joined)
	if err != nil {
		t.Fatal(err)
	}

	if parsed.Path != "/assets/nested/hero?#.webp" {
		t.Fatalf("expected reserved characters to stay in the path, got %q", parsed.Path)
	}
	if parsed.RawQuery != "" {
		t.Fatalf("expected no query string, got %q", parsed.RawQuery)
	}
	if parsed.Fragment != "" {
		t.Fatalf("expected no fragment, got %q", parsed.Fragment)
	}
}

func TestValidateDeployPlanRejectsEscapingVerifyObjectKey(t *testing.T) {
	err := validateDeployPlan(DeployPlan{
		Verify: DeliveryVerifyPlan{
			Enabled: true,
			Checks: []VerifyCheck{
				{ObjectKey: "../outside.txt"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected escaping verify object key to fail validation")
	}
	if !strings.Contains(err.Error(), "invalid verify object key") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateDeployPlanRejectsEscapingUploadLocalPath(t *testing.T) {
	planBaseDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.bin")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		localPath string
	}{
		{
			name:      "relative traversal",
			localPath: "../outside.bin",
		},
		{
			name:      "absolute outside root",
			localPath: outside,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDeployPlan(DeployPlan{
				baseDir: planBaseDir,
				Uploads: []UploadRequest{
					{
						LocalPath:     tc.localPath,
						ObjectKey:     "assets/hero.webp",
						ContentSHA256: strings.Repeat("0", 64),
					},
				},
			})
			if err == nil {
				t.Fatal("expected escaping upload local_path to fail validation")
			}
			if !strings.Contains(err.Error(), "invalid upload local_path") {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestValidateDeployPlanRejectsUploadWithoutContentHash(t *testing.T) {
	err := validateDeployPlan(DeployPlan{
		baseDir: t.TempDir(),
		Uploads: []UploadRequest{
			{
				LocalPath: "artifacts/hero.webp",
				ObjectKey: "assets/hero.webp",
			},
		},
	})
	if err == nil {
		t.Fatal("expected missing upload content_sha256 to fail validation")
	}
	if !strings.Contains(err.Error(), "upload content_sha256") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateDeployPlanRejectsDisabledVerifyWithChecks(t *testing.T) {
	err := validateDeployPlan(DeployPlan{
		Verify: DeliveryVerifyPlan{
			Enabled: false,
			Checks: []VerifyCheck{
				{ObjectKey: "assets/hero.webp"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected verify-disabled plan with checks to fail validation")
	}
	if !strings.Contains(err.Error(), "verify checks") || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateDeployPlanRejectsEnabledVerifyWithoutChecks(t *testing.T) {
	err := validateDeployPlan(DeployPlan{
		Verify: DeliveryVerifyPlan{
			Enabled: true,
		},
	})
	if err == nil {
		t.Fatal("expected verify-enabled plan without checks to fail validation")
	}
	if !strings.Contains(err.Error(), "enables verify") || !strings.Contains(err.Error(), "no checks") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateDeployPlanRejectsEscapingOriginRootDir(t *testing.T) {
	planBaseDir := t.TempDir()
	err := validateDeployPlan(DeployPlan{
		baseDir: planBaseDir,
		Origin: OriginTarget{
			Provider: "local",
			RootDir:  "../outside-origin",
		},
	})
	if err == nil {
		t.Fatal("expected escaping origin root_dir to fail validation")
	}
	if !strings.Contains(err.Error(), "invalid origin root_dir") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateDeployPlanRejectsVerifyURLOutsideCDNBaseURL(t *testing.T) {
	err := validateDeployPlan(DeployPlan{
		CDN: CDNTarget{
			BaseURL: "https://cdn.example.com/assets",
		},
		Verify: DeliveryVerifyPlan{
			Enabled: true,
			Checks: []VerifyCheck{
				{URL: "https://evil.example.com/assets/release-manifest.json"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected verify url outside cdn.base_url to fail validation")
	}
	if !strings.Contains(err.Error(), "verify url") || !strings.Contains(err.Error(), "cdn.base_url") {
		t.Fatalf("unexpected verify url validation error: %v", err)
	}
}

func TestValidateDeployPlanRejectsVerifyURLWhenCDNBaseURLEmpty(t *testing.T) {
	err := validateDeployPlan(DeployPlan{
		Verify: DeliveryVerifyPlan{
			Enabled: true,
			Checks: []VerifyCheck{
				{URL: "https://cdn.example.com/assets/release-manifest.json"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected verify url without cdn.base_url to fail validation")
	}
	if !strings.Contains(err.Error(), "cdn.base_url is empty") {
		t.Fatalf("unexpected verify url validation error: %v", err)
	}
}

func TestRunDeliveryVerifyPlanRejectsRedirectOutsideCDNBaseURL(t *testing.T) {
	var escapedHits atomic.Int32
	escapedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer escapedServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, escapedServer.URL+"/outside", http.StatusFound)
	}))
	defer redirectServer.Close()

	plan := DeployPlan{
		CDN: CDNTarget{
			BaseURL: redirectServer.URL + "/assets",
		},
		Verify: DeliveryVerifyPlan{
			Enabled: true,
			Checks: []VerifyCheck{
				{URL: redirectServer.URL + "/assets/hero.webp"},
			},
		},
	}
	if err := validateDeployPlan(plan); err != nil {
		t.Fatal(err)
	}

	_, err := runDeliveryVerifyPlan(context.Background(), plan, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected redirect outside cdn.base_url to fail verification")
	}
	if !strings.Contains(err.Error(), "cdn.base_url") {
		t.Fatalf("unexpected verify redirect error: %v", err)
	}
	if got := escapedHits.Load(); got != 0 {
		t.Fatalf("expected redirect destination not to be fetched, got %d hits", got)
	}
}

func TestResolveVerifyCheckURLRejectsEscapingObjectKey(t *testing.T) {
	planBaseDir := t.TempDir()
	originRoot := filepath.Join(planBaseDir, "origin")
	if err := os.MkdirAll(originRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := resolveVerifyCheckURL(DeployPlan{
		baseDir: planBaseDir,
		Origin: OriginTarget{
			Provider: "local",
			RootDir:  "origin",
		},
	}, VerifyCheck{
		ObjectKey: "../outside.txt",
	})
	if err == nil {
		t.Fatal("expected escaping verify object key to fail resolution")
	}
	if !strings.Contains(err.Error(), "invalid verify object key") {
		t.Fatalf("unexpected resolve error: %v", err)
	}
}

func TestResolveVerifyCheckURLRejectsSymlinkEscape(t *testing.T) {
	planBaseDir := t.TempDir()
	originRoot := filepath.Join(planBaseDir, "origin")
	outsideRoot := t.TempDir()
	if err := os.MkdirAll(originRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideRoot, filepath.Join(originRoot, "assets")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := resolveVerifyCheckURL(DeployPlan{
		baseDir: planBaseDir,
		Origin: OriginTarget{
			Provider: "local",
			RootDir:  "origin",
		},
	}, VerifyCheck{
		ObjectKey: "assets/outside.txt",
	})
	if err == nil {
		t.Fatal("expected symlink verify object key to fail resolution")
	}
	if !strings.Contains(err.Error(), "verify object key") || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("unexpected symlink resolve error: %v", err)
	}
}

func TestResolveUploadRequestRejectsEscapingLocalPath(t *testing.T) {
	planBaseDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.bin")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveUploadRequest(planBaseDir, UploadRequest{
		LocalPath: outside,
		ObjectKey: "assets/hero.webp",
	})
	if err == nil {
		t.Fatal("expected escaping upload local_path to fail resolution")
	}
	if !strings.Contains(err.Error(), "upload local_path") || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("unexpected resolve error: %v", err)
	}
}

func TestResolveUploadRequestRejectsSymlinkEscape(t *testing.T) {
	planBaseDir := t.TempDir()
	outsideRoot := t.TempDir()
	if err := os.Symlink(outsideRoot, filepath.Join(planBaseDir, "artifacts")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := resolveUploadRequest(planBaseDir, UploadRequest{
		LocalPath: "artifacts/outside.bin",
		ObjectKey: "assets/hero.webp",
	})
	if err == nil {
		t.Fatal("expected symlinked upload local_path to fail resolution")
	}
	if !strings.Contains(err.Error(), "upload local_path") || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("unexpected resolve error: %v", err)
	}
}

func TestLocalOriginAdapterRejectsSymlinkEscapeTargetPath(t *testing.T) {
	originRoot := t.TempDir()
	outsideRoot := t.TempDir()
	if err := os.Symlink(outsideRoot, filepath.Join(originRoot, "assets")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	adapter := &LocalOriginAdapter{RootDir: originRoot}
	_, err := adapter.targetPath("assets/outside.txt")
	if err == nil {
		t.Fatal("expected symlinked object key to fail resolution")
	}
	if !strings.Contains(err.Error(), "object key") || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("unexpected target path error: %v", err)
	}
}

func TestLocalOriginAdapterUploadRejectsSourceHashMismatch(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "hero.webp")
	if err := os.WriteFile(sourcePath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	contentHash, _, err := hashFileContext(context.Background(), sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("mutated"), 0o644); err != nil {
		t.Fatal(err)
	}

	adapter := &LocalOriginAdapter{RootDir: t.TempDir()}
	_, err = adapter.Upload(context.Background(), UploadRequest{
		LocalPath:      sourcePath,
		ObjectKey:      "assets/hero.webp",
		ContentSHA256:  contentHash,
		SkipIfSameHash: true,
	})
	if err == nil {
		t.Fatal("expected upload with mismatched content hash to fail")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("unexpected upload error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(adapter.RootDir, "assets", "hero.webp")); !os.IsNotExist(statErr) {
		t.Fatalf("expected target not to be written, got err=%v", statErr)
	}
}

func TestResolveConversionEntryPathsRejectsEscapingArtifactRoots(t *testing.T) {
	artifactDir := t.TempDir()
	sourceRoot := filepath.Join(artifactDir, "source")
	outputRoot := filepath.Join(artifactDir, "output")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeJPEG(t, filepath.Join(sourceRoot, "hero.jpg"), 800, 400)
	if err := os.WriteFile(filepath.Join(outputRoot, "hero.webp"), []byte("webp"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(artifactDir, "outside.bin")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := ConversionManifest{
		manifestPath: filepath.Join(artifactDir, "conversion-manifest.json"),
		RootDir:      "source",
		OutputDir:    "output",
	}

	tests := []struct {
		name  string
		entry ManifestEntry
		want  string
	}{
		{
			name: "source",
			entry: ManifestEntry{
				RelativePath: "hero.jpg",
				SourcePath:   "../outside.bin",
				OutputPath:   "hero.webp",
			},
			want: "source path",
		},
		{
			name: "output",
			entry: ManifestEntry{
				RelativePath: "hero.jpg",
				SourcePath:   "hero.jpg",
				OutputPath:   "../outside.bin",
			},
			want: "output path",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := resolveConversionEntryPaths(manifest, tc.entry, "")
			if err == nil {
				t.Fatal("expected escaping artifact path to fail")
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "escapes root") {
				t.Fatalf("unexpected escape error: %v", err)
			}
		})
	}
}

func TestRunPlanRejectsVariantOutputPathOutsideManifestRoot(t *testing.T) {
	artifactDir := filepath.Join(t.TempDir(), "artifact")
	sourceRoot := filepath.Join(artifactDir, "source")
	outputRoot := filepath.Join(artifactDir, "output")
	originRoot := filepath.Join(artifactDir, "origin")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeJPEG(t, filepath.Join(sourceRoot, "hero.jpg"), 1600, 900)
	if err := os.WriteFile(filepath.Join(outputRoot, "hero.webp"), []byte("primary"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "outside.webp"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	conversionManifestPath := filepath.Join(artifactDir, "conversion-manifest.json")
	err := writeJSONFile(context.Background(), conversionManifestPath, ConversionManifest{
		Version:     1,
		GeneratedAt: "2026-04-13T00:00:00Z",
		Command:     "bulk",
		RootDir:     "source",
		OutputDir:   "output",
		Entries: []ManifestEntry{
			{
				RelativePath: "hero.jpg",
				SourcePath:   "hero.jpg",
				OutputPath:   "hero.webp",
				Width:        1600,
				Height:       900,
				OutputWidth:  1600,
				OutputHeight: 900,
				Quality:      75,
				OutputVariants: []OutputVariantInfo{
					{
						Name:         "16x9",
						Usage:        variantUsagePrimary,
						AspectRatio:  "16:9",
						OutputPath:   "../outside.webp",
						OutputWidth:  1600,
						OutputHeight: 900,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = RunPlan(context.Background(), PlanConfig{
		ConversionManifestPath: conversionManifestPath,
		ReleaseManifestPath:    filepath.Join(artifactDir, "release-manifest.json"),
		DeployPlanPath:         filepath.Join(artifactDir, "deploy-plan.dev.json"),
		Environment:            "dev",
		OriginProvider:         "local",
		OriginRoot:             originRoot,
		CDNProvider:            "noop",
		ImmutablePrefix:        "assets",
		MutablePrefix:          "release",
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected variant output_path outside output root to fail")
	}
	if !strings.Contains(err.Error(), "variant output path") || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("unexpected variant output path error: %v", err)
	}
}

func TestRunPlanRejectsReleaseManifestOutsideArtifactRoot(t *testing.T) {
	artifactDir := filepath.Join(t.TempDir(), "artifact")
	releaseManifest := filepath.Join(t.TempDir(), "release-manifest.json")
	deployPlanPath := filepath.Join(artifactDir, "deploy-plan.dev.json")

	var stdout bytes.Buffer
	_, err := RunPlan(context.Background(), PlanConfig{
		ConversionManifestPath: filepath.Join(artifactDir, "conversion-manifest.json"),
		ReleaseManifestPath:    releaseManifest,
		DeployPlanPath:         deployPlanPath,
		Environment:            "dev",
		OriginProvider:         "local",
		CDNProvider:            "noop",
		ImmutablePrefix:        "assets",
		MutablePrefix:          "release",
	}, &stdout)
	if err == nil {
		t.Fatal("expected release manifest outside artifact root to fail")
	}
	if !strings.Contains(err.Error(), "release manifest path") || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("unexpected plan error: %v", err)
	}
	if _, statErr := os.Stat(releaseManifest); !os.IsNotExist(statErr) {
		t.Fatalf("expected no release manifest to be written outside artifact root, got err=%v", statErr)
	}
}

func TestRunPlanRejectsOriginRootOutsideArtifactRoot(t *testing.T) {
	artifactDir := filepath.Join(t.TempDir(), "artifact")
	deployPlanPath := filepath.Join(artifactDir, "deploy-plan.dev.json")
	releaseManifest := filepath.Join(artifactDir, "release-manifest.json")
	originRoot := filepath.Join(t.TempDir(), "origin")

	var stdout bytes.Buffer
	_, err := RunPlan(context.Background(), PlanConfig{
		ConversionManifestPath: filepath.Join(artifactDir, "conversion-manifest.json"),
		ReleaseManifestPath:    releaseManifest,
		DeployPlanPath:         deployPlanPath,
		Environment:            "dev",
		OriginProvider:         "local",
		OriginRoot:             originRoot,
		CDNProvider:            "noop",
		ImmutablePrefix:        "assets",
		MutablePrefix:          "release",
	}, &stdout)
	if err == nil {
		t.Fatal("expected origin root outside artifact root to fail")
	}
	if !strings.Contains(err.Error(), "origin root") || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("unexpected plan error: %v", err)
	}
}

func TestRunProcessCommandHandlesLargeBatchWithOutDir(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "bulk-out")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 1000; i++ {
		path := filepath.Join(root, "png-1000", "white-"+strconv.FormatInt(int64(i), 10)+".png")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		writePNG(t, path, 16+(i%4)*8, 16+(i%4)*8)
	}

	cfg := testConfig(root)
	cfg.OutDir = outDir
	cfg.Workers = 4

	var stdout bytes.Buffer
	summary, err := RunProcessCommand(context.Background(), cfg, newDimensionAwareFakeEncoder(t, 48, nil), &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Converted != 1000 || summary.Failed != 0 || summary.Rejected != 0 {
		t.Fatalf("expected a clean 1000-file batch, got %#v", summary)
	}
	if _, err := os.Stat(filepath.Join(outDir, "png-1000", "white-999.webp")); err != nil {
		t.Fatalf("expected last generated output to exist: %v", err)
	}
}
