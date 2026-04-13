package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type CropMode string

const (
	cropModeSafe  CropMode = "safe"
	cropModeFocus CropMode = "focus"
)

const (
	variantUsagePrimary    = "primary"
	variantUsageSupporting = "supporting"
)

type AspectVariantConfig struct {
	Name        string
	AspectRatio string
	Numerator   int
	Denominator int
}

type OutputVariantInfo struct {
	Name            string  `json:"name,omitempty"`
	Usage           string  `json:"usage,omitempty"`
	AspectRatio     string  `json:"aspect_ratio,omitempty"`
	OutputPath      string  `json:"output_path"`
	OutputWidth     int     `json:"output_width"`
	OutputHeight    int     `json:"output_height"`
	OutputSizeBytes int64   `json:"output_size_bytes,omitempty"`
	SavedBytes      int64   `json:"saved_bytes,omitempty"`
	SavedPercent    float64 `json:"saved_percent,omitempty"`
	Resized         bool    `json:"resized,omitempty"`
	Cropped         bool    `json:"cropped,omitempty"`
}

type outputVariantPlan struct {
	Name        string
	Usage       string
	AspectRatio string
	Numerator   int
	Denominator int
	OutputPath  string
}

func normalizeCropMode(raw string) (CropMode, error) {
	value := CropMode(strings.ToLower(strings.TrimSpace(raw)))
	switch value {
	case "", cropModeSafe:
		return cropModeSafe, nil
	case cropModeFocus:
		return cropModeFocus, nil
	default:
		return "", fmt.Errorf("unsupported crop-mode %q", raw)
	}
}

func parseAspectVariants(raw string) ([]AspectVariantConfig, error) {
	items := splitCommaList([]string{raw})
	if len(items) == 0 {
		return nil, nil
	}

	variants := make([]AspectVariantConfig, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		variant, err := parseAspectVariant(item)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[variant.Name]; ok {
			return nil, fmt.Errorf("duplicate aspect variant %q", item)
		}
		seen[variant.Name] = struct{}{}
		variants = append(variants, variant)
	}
	return variants, nil
}

func parseAspectVariant(raw string) (AspectVariantConfig, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	value = strings.ReplaceAll(value, ":", "x")
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return AspectVariantConfig{}, fmt.Errorf("invalid aspect variant %q", raw)
	}

	numerator, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || numerator <= 0 {
		return AspectVariantConfig{}, fmt.Errorf("invalid aspect variant %q", raw)
	}
	denominator, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || denominator <= 0 {
		return AspectVariantConfig{}, fmt.Errorf("invalid aspect variant %q", raw)
	}

	divisor := gcd(numerator, denominator)
	numerator /= divisor
	denominator /= divisor

	return AspectVariantConfig{
		Name:        fmt.Sprintf("%dx%d", numerator, denominator),
		AspectRatio: fmt.Sprintf("%d:%d", numerator, denominator),
		Numerator:   numerator,
		Denominator: denominator,
	}, nil
}

func gcd(a int, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

func aspectVariantNames(variants []AspectVariantConfig) []string {
	names := make([]string, 0, len(variants))
	for _, variant := range variants {
		names = append(names, variant.Name)
	}
	return names
}

func buildOutputVariantPlans(cfg ProcessConfig, job FileJob) []outputVariantPlan {
	if len(cfg.AspectVariants) == 0 {
		return []outputVariantPlan{{
			Usage:      variantUsagePrimary,
			OutputPath: buildOutputPath(cfg, job),
		}}
	}

	plans := make([]outputVariantPlan, 0, len(cfg.AspectVariants))
	for index, variant := range cfg.AspectVariants {
		usage := variantUsageSupporting
		primary := false
		if index == 0 {
			usage = variantUsagePrimary
			primary = true
		}
		plans = append(plans, outputVariantPlan{
			Name:        variant.Name,
			Usage:       usage,
			AspectRatio: variant.AspectRatio,
			Numerator:   variant.Numerator,
			Denominator: variant.Denominator,
			OutputPath:  buildOutputPathForVariant(cfg, job, variant.Name, primary),
		})
	}
	return plans
}

func buildOutputPathForVariant(cfg ProcessConfig, job FileJob, variantName string, primary bool) string {
	suffix := ""
	if !primary && strings.TrimSpace(variantName) != "" {
		suffix = "." + variantName
	}

	if cfg.OutDir == "" {
		base := strings.TrimSuffix(job.Path, filepath.Ext(job.Path))
		return base + suffix + ".webp"
	}

	relativePath := strings.TrimSuffix(filepath.FromSlash(job.RelativePath), filepath.Ext(job.RelativePath))
	return filepath.Join(cfg.OutDir, relativePath+suffix+".webp")
}

func primaryOutputVariant(variants []OutputVariantInfo) (OutputVariantInfo, bool) {
	if len(variants) == 0 {
		return OutputVariantInfo{}, false
	}
	for _, variant := range variants {
		if variant.Usage == variantUsagePrimary {
			return variant, true
		}
	}
	return variants[0], true
}

func manifestEntryOutputVariants(entry ManifestEntry) []OutputVariantInfo {
	if len(entry.OutputVariants) > 0 {
		return append([]OutputVariantInfo(nil), entry.OutputVariants...)
	}
	if strings.TrimSpace(entry.OutputPath) == "" {
		return nil
	}

	return []OutputVariantInfo{{
		Usage:           variantUsagePrimary,
		OutputPath:      entry.OutputPath,
		OutputWidth:     entry.OutputWidth,
		OutputHeight:    entry.OutputHeight,
		OutputSizeBytes: entry.OutputSizeBytes,
		SavedBytes:      entry.SavedBytes,
		SavedPercent:    entry.SavedPercent,
		Resized:         entry.Resized,
	}}
}
