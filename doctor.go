package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	doctorStatusPass = "pass"
	doctorStatusWarn = "warn"
	doctorStatusFail = "fail"
)

type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type DoctorSummary struct {
	OK       bool          `json:"ok"`
	Passed   int           `json:"passed"`
	Warnings int           `json:"warnings"`
	Failed   int           `json:"failed"`
	Checks   []DoctorCheck `json:"checks"`
}

type doctorConfigResult struct {
	Check   DoctorCheck
	Loaded  bool
	Config  fileConfig
	BaseDir string
}

type doctorConfigPathTarget struct {
	Label   string
	Path    string
	WantDir bool
}

var (
	doctorLookPath = exec.LookPath
	doctorRunCmd   = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		return cmd.CombinedOutput()
	}
)

func runDoctorCommand(ctx context.Context, args []string, encoder Encoder, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)

	selection := configSelection{}
	bindConfigFlags(fs, &selection)
	var jsonOutput bool
	fs.BoolVar(&jsonOutput, "json", false, "Emit the doctor report as JSON on stdout")
	fs.Usage = func() {
		printDoctorUsage(stderr)
	}

	helpShown, err := parseCommandFlags(fs, args)
	if err != nil {
		return exitConfigError, err
	}
	if helpShown {
		return exitOK, nil
	}
	if selection.NoConfig && strings.TrimSpace(selection.Path) != "" {
		return exitConfigError, fmt.Errorf("-config and -no-config cannot be used together")
	}
	if err := ctx.Err(); err != nil {
		return exitInterrupted, err
	}

	summary, err := runDoctor(ctx, selection, encoder)
	if err != nil {
		return exitConfigError, err
	}

	if jsonOutput {
		if err := writeJSONValue(stdout, summary); err != nil {
			return exitConfigError, err
		}
	} else {
		printDoctorSummary(stdout, summary)
	}

	if summary.Failed > 0 {
		return exitConfigError, nil
	}
	return exitOK, nil
}

func runDoctor(ctx context.Context, selection configSelection, encoder Encoder) (DoctorSummary, error) {
	checks := make([]DoctorCheck, 0, 8)

	cwd, err := os.Getwd()
	if err != nil {
		checks = append(checks, DoctorCheck{Name: "cwd", Status: doctorStatusFail, Detail: err.Error()})
	} else {
		checks = append(checks, DoctorCheck{Name: "cwd", Status: doctorStatusPass, Detail: cwd})
	}

	if err := ctx.Err(); err != nil {
		return summarizeDoctorChecks(checks), err
	}

	configResult := resolveDoctorConfig(cwd, selection)
	checks = append(checks, configResult.Check)
	if configResult.Loaded {
		checks = append(checks, doctorConfigPathsCheck(configResult.Config, configResult.BaseDir))
	}
	checks = append(checks, doctorEncoderChecks(ctx, encoder)...)
	checks = append(checks, doctorTempDirCheck())
	checks = append(checks, DoctorCheck{
		Name:   "cpus",
		Status: doctorStatusPass,
		Detail: fmt.Sprintf("%d logical CPUs available", runtime.NumCPU()),
	})

	return summarizeDoctorChecks(checks), nil
}

func resolveDoctorConfig(cwd string, selection configSelection) doctorConfigResult {
	if selection.NoConfig {
		return doctorConfigResult{
			Check: DoctorCheck{
				Name:   "config",
				Status: doctorStatusWarn,
				Detail: "config loading disabled by -no-config",
			},
		}
	}

	var (
		path  string
		label string
	)
	if strings.TrimSpace(selection.Path) != "" {
		absPath, err := filepath.Abs(selection.Path)
		if err != nil {
			return doctorConfigResult{
				Check: DoctorCheck{Name: "config", Status: doctorStatusFail, Detail: err.Error()},
			}
		}
		path = absPath
		label = "explicit"
	} else {
		discoveredPath, found, err := discoverConfigFile(cwd)
		if err != nil {
			return doctorConfigResult{
				Check: DoctorCheck{Name: "config", Status: doctorStatusFail, Detail: err.Error()},
			}
		}
		if !found {
			return doctorConfigResult{
				Check: DoctorCheck{
					Name:   "config",
					Status: doctorStatusWarn,
					Detail: "no webp-guard.toml found from the current directory upward; run `webp-guard init` when you want project defaults",
				},
			}
		}
		path = discoveredPath
		label = "discovered"
	}

	cfg, err := readConfigFile(path)
	if err != nil {
		return doctorConfigResult{
			Check: DoctorCheck{Name: "config", Status: doctorStatusFail, Detail: fmt.Sprintf("%s config %s is invalid: %v", label, path, err)},
		}
	}

	schema := cfg.SchemaVersion
	if schema == 0 {
		schema = configSchemaVersion
	}
	return doctorConfigResult{
		Check: DoctorCheck{
			Name:   "config",
			Status: doctorStatusPass,
			Detail: fmt.Sprintf("%s config %s loaded (schema_version=%d)", label, path, schema),
		},
		Loaded:  true,
		Config:  cfg,
		BaseDir: filepath.Dir(path),
	}
}

func doctorConfigPathsCheck(cfg fileConfig, baseDir string) DoctorCheck {
	targets := collectDoctorConfigPathTargets(cfg, baseDir)
	if len(targets) == 0 {
		return DoctorCheck{
			Name:   "config-paths",
			Status: doctorStatusPass,
			Detail: "no representative input paths configured",
		}
	}

	present := 0
	presentEntries := make([]string, 0)
	missing := make([]string, 0)
	invalid := make([]string, 0)

	for _, target := range targets {
		info, err := os.Stat(target.Path)
		if err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, fmt.Sprintf("%s=%s", target.Label, target.Path))
				continue
			}
			invalid = append(invalid, fmt.Sprintf("%s=%s (%v)", target.Label, target.Path, err))
			continue
		}
		if target.WantDir && !info.IsDir() {
			invalid = append(invalid, fmt.Sprintf("%s=%s (expected directory)", target.Label, target.Path))
			continue
		}
		if !target.WantDir && info.IsDir() {
			invalid = append(invalid, fmt.Sprintf("%s=%s (expected file)", target.Label, target.Path))
			continue
		}
		present++
		presentEntries = append(presentEntries, fmt.Sprintf("%s=%s", target.Label, target.Path))
	}

	detail := fmt.Sprintf("checked %d representative paths; %d present", len(targets), present)
	if len(presentEntries) > 0 {
		detail += "; present: " + strings.Join(presentEntries, ", ")
	}
	if len(missing) > 0 {
		detail += "; missing: " + strings.Join(missing, ", ")
	}
	if len(invalid) > 0 {
		detail += "; invalid: " + strings.Join(invalid, ", ")
	}

	switch {
	case len(invalid) > 0:
		return DoctorCheck{Name: "config-paths", Status: doctorStatusFail, Detail: detail}
	case len(missing) > 0:
		return DoctorCheck{Name: "config-paths", Status: doctorStatusWarn, Detail: detail}
	default:
		return DoctorCheck{Name: "config-paths", Status: doctorStatusPass, Detail: detail}
	}
}

func collectDoctorConfigPathTargets(cfg fileConfig, baseDir string) []doctorConfigPathTarget {
	candidates := []doctorConfigPathTarget{
		doctorOptionalPathTarget("process.dir", baseDir, cfg.Process.Dir, true),
		doctorOptionalPathTarget("resume.resume_from", baseDir, cfg.Resume.ResumeFrom, false),
		doctorOptionalPathTarget("verify.manifest", baseDir, cfg.Verify.Manifest, false),
		doctorOptionalPathTarget("plan.conversion_manifest", baseDir, cfg.Plan.ConversionManifest, false),
		doctorOptionalPathTarget("publish.plan", baseDir, cfg.Publish.Plan, false),
		doctorOptionalPathTarget("verify_delivery.plan", baseDir, cfg.VerifyDelivery.Plan, false),
	}

	targets := make([]doctorConfigPathTarget, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Path == "" {
			continue
		}
		targets = append(targets, candidate)
	}
	return targets
}

func doctorOptionalPathTarget(label string, baseDir string, value *string, wantDir bool) doctorConfigPathTarget {
	if value == nil {
		return doctorConfigPathTarget{}
	}
	path := resolveConfigPath(baseDir, *value)
	if path == "" {
		return doctorConfigPathTarget{}
	}
	return doctorConfigPathTarget{
		Label:   label,
		Path:    path,
		WantDir: wantDir,
	}
}

func doctorEncoderChecks(ctx context.Context, encoder Encoder) []DoctorCheck {
	switch value := encoder.(type) {
	case *CWebPEncoder:
		path, err := doctorLookPath(value.Binary)
		if err != nil {
			return []DoctorCheck{
				{Name: "encoder", Status: doctorStatusFail, Detail: fmt.Sprintf("%s not found in PATH", value.Binary)},
			}
		}
		return []DoctorCheck{
			{Name: "encoder", Status: doctorStatusPass, Detail: fmt.Sprintf("%s found at %s", value.Binary, path)},
			doctorEncoderVersionCheck(ctx, path, value.Binary),
		}
	default:
		if err := encoder.Check(); err != nil {
			return []DoctorCheck{
				{Name: "encoder", Status: doctorStatusFail, Detail: err.Error()},
			}
		}
		return []DoctorCheck{
			{Name: "encoder", Status: doctorStatusPass, Detail: "encoder check passed"},
		}
	}
}

func doctorEncoderVersionCheck(ctx context.Context, path string, binary string) DoctorCheck {
	output, err := doctorRunCmd(ctx, path, "-version")
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return DoctorCheck{
			Name:   "encoder-version",
			Status: doctorStatusFail,
			Detail: fmt.Sprintf("%s -version failed: %s", binary, detail),
		}
	}

	version := strings.TrimSpace(string(output))
	if version == "" {
		return DoctorCheck{
			Name:   "encoder-version",
			Status: doctorStatusWarn,
			Detail: fmt.Sprintf("%s -version returned no output", binary),
		}
	}
	version = strings.SplitN(version, "\n", 2)[0]
	return DoctorCheck{
		Name:   "encoder-version",
		Status: doctorStatusPass,
		Detail: fmt.Sprintf("%s %s", binary, version),
	}
}

func doctorTempDirCheck() DoctorCheck {
	file, err := os.CreateTemp("", "webp-guard-doctor-*")
	if err != nil {
		return DoctorCheck{Name: "temp-dir", Status: doctorStatusFail, Detail: err.Error()}
	}
	path := file.Name()
	closeQuietly(file)
	if err := os.Remove(path); err != nil {
		return DoctorCheck{Name: "temp-dir", Status: doctorStatusFail, Detail: err.Error()}
	}
	return DoctorCheck{
		Name:   "temp-dir",
		Status: doctorStatusPass,
		Detail: fmt.Sprintf("%s is writable", filepath.Dir(path)),
	}
}

func summarizeDoctorChecks(checks []DoctorCheck) DoctorSummary {
	summary := DoctorSummary{Checks: checks}
	for _, check := range checks {
		switch check.Status {
		case doctorStatusPass:
			summary.Passed++
		case doctorStatusWarn:
			summary.Warnings++
		case doctorStatusFail:
			summary.Failed++
		}
	}
	summary.OK = summary.Failed == 0
	return summary
}

func printDoctorSummary(w io.Writer, summary DoctorSummary) {
	writeLine(w, "Doctor report")
	for _, check := range summary.Checks {
		writef(w, "  [%s] %s: %s\n", check.Status, check.Name, check.Detail)
	}
	writeLine(w)
	writef(w, "Summary: %d passed, %d warnings, %d failed\n", summary.Passed, summary.Warnings, summary.Failed)
}
