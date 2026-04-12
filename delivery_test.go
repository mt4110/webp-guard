package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
