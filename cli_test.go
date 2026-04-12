package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBulkJSONWritesSummaryToStdoutAndLogsToStderr(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "hero.jpg")
	writeJPEG(t, source, 1600, 800)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{
		"bulk",
		"-dir", root,
		"-dry-run",
		"-workers", "1",
		"-cpus", "1",
		"-json",
	}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d", code)
	}

	var summary Summary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("expected JSON summary on stdout, got %q: %v", stdout.String(), err)
	}
	if summary.Total != 1 || summary.DryRun != 1 {
		t.Fatalf("unexpected summary %#v", summary)
	}
	if strings.Contains(stdout.String(), "Starting bulk") {
		t.Fatalf("stdout should stay machine-readable, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Starting bulk") {
		t.Fatalf("stderr should contain human-readable logs, got %q", stderr.String())
	}
}

func TestRunBulkUsesAutoDiscoveredConfigRelativeToConfigFile(t *testing.T) {
	root := t.TempDir()
	assetsDir := filepath.Join(root, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, filepath.Join(assetsDir, "hero.jpg"), 1600, 800)

	config := `schema_version = 1

[process]
dir = "./assets"
cpus = "1"
workers = "1"
report = "./out/configured-report.jsonl"

[bulk]
dry_run = true
`
	if err := os.WriteFile(filepath.Join(root, defaultConfigFileName), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	nested := filepath.Join(root, "sub", "dir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{"bulk", "-json"}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d", code)
	}

	var summary Summary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("expected JSON summary on stdout, got %q: %v", stdout.String(), err)
	}
	if summary.Total != 1 || summary.DryRun != 1 {
		t.Fatalf("unexpected summary %#v", summary)
	}

	reportPath := filepath.Join(root, "out", "configured-report.jsonl")
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("expected report path resolved relative to config file, got %v", err)
	}
	if !strings.Contains(stderr.String(), "Starting bulk on "+filepath.Join(root, "assets")) {
		t.Fatalf("expected logs to use config-resolved root, got %q", stderr.String())
	}
}

func TestCLIFlagsOverrideConfigFile(t *testing.T) {
	root := t.TempDir()
	emptyDir := filepath.Join(root, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	imageDir := filepath.Join(root, "images")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, filepath.Join(imageDir, "hero.jpg"), 1600, 800)

	config := `schema_version = 1

[process]
dir = "./empty"
cpus = "1"
workers = "1"

[bulk]
dry_run = true
`
	if err := os.WriteFile(filepath.Join(root, defaultConfigFileName), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{
		"bulk",
		"-dir", imageDir,
		"-json",
	}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d", code)
	}

	var summary Summary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("expected JSON summary on stdout, got %q: %v", stdout.String(), err)
	}
	if summary.Total != 1 {
		t.Fatalf("expected CLI flag to override config dir, got %#v", summary)
	}
}

func TestInitCommandWritesStarterConfig(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{"init"}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected init to keep stdout empty, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Wrote") {
		t.Fatalf("expected init to report the generated file on stderr, got %q", stderr.String())
	}

	configBytes, err := os.ReadFile(filepath.Join(root, defaultConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"schema_version = 1", "[process]", "[plan]", "[publish]"} {
		if !strings.Contains(string(configBytes), needle) {
			t.Fatalf("expected generated config to contain %q", needle)
		}
	}
}

func TestCompletionCommandWritesScriptToStdout(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{"completion", "bash"}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected completion to keep stderr empty, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "complete -F _webp_guard webp-guard") {
		t.Fatalf("expected bash completion script, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "version)\n\t\t\tflags=\"-h --help\"") {
		t.Fatalf("expected version flags in bash completion script, got %q", stdout.String())
	}
}

func TestRootCompletionFlagsIncludeVersionAliases(t *testing.T) {
	words := completionWords(rootCompletionFlags)
	for _, needle := range []string{"-version", "--version", "--dir", "--dry-run", "--out-dir"} {
		if !strings.Contains(words, needle) {
			t.Fatalf("expected root completion flags to include %q, got %v", needle, rootCompletionFlags)
		}
	}
}

func TestBashCompletionKeepsCompletionFlagsCompletable(t *testing.T) {
	script := renderBashCompletionScript()
	if strings.Contains(script, `-shell|completion)`) {
		t.Fatalf("expected completion subcommand to stop masquerading as a value flag, got %q", script)
	}
	if !strings.Contains(script, "-shell)") {
		t.Fatalf("expected bash completion to keep -shell value suggestions, got %q", script)
	}
}

func TestFishCompletionKeepsCompletionFlagsCompletable(t *testing.T) {
	script := renderFishCompletionScript()
	if !strings.Contains(script, `__webp_guard_prev_arg_in completion -shell --shell; and not string match -qr "^-" -- (commandline -ct)`) {
		t.Fatalf("expected fish completion to gate shell suggestions to positional args, got %q", script)
	}
}

func TestPowerShellCompletionIncludesVersionFlags(t *testing.T) {
	script := renderPowerShellCompletionScript()
	if !strings.Contains(script, `"version" = @("-h", "--help")`) {
		t.Fatalf("expected version flags in PowerShell completion script, got %q", script)
	}
}

func TestPowerShellCompletionKeepsCompletionFlagsCompletable(t *testing.T) {
	script := renderPowerShellCompletionScript()
	if strings.Contains(script, `"completion" { $candidates = $shells; break }`) {
		t.Fatalf("expected PowerShell completion to reserve shell suggestions for positional args, got %q", script)
	}
}

func TestCompletionValueCasesIncludeDoubleDashFlags(t *testing.T) {
	bash := renderBashCompletionScript()
	for _, needle := range []string{"-dir|--dir", "-shell|--shell", "-dry-run|--dry-run"} {
		if !strings.Contains(bash, needle) {
			t.Fatalf("expected bash completion to include %q, got %q", needle, bash)
		}
	}

	fish := renderFishCompletionScript()
	for _, needle := range []string{"__webp_guard_prev_arg_in -dir --dir", "__webp_guard_prev_arg_in -crop-mode --crop-mode", "__webp_guard_prev_arg_in -dry-run --dry-run"} {
		if !strings.Contains(fish, needle) {
			t.Fatalf("expected fish completion to include %q, got %q", needle, fish)
		}
	}

	powershell := renderPowerShellCompletionScript()
	for _, needle := range []string{`"--shell" { $candidates = $shells; break }`, `"--dry-run" {`, `"--crop-mode" { $candidates = @("safe", "focus"); break }`} {
		if !strings.Contains(powershell, needle) {
			t.Fatalf("expected PowerShell completion to include %q, got %q", needle, powershell)
		}
	}
}

func TestHelpCommandPrintsSubcommandUsage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{"help", "completion"}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Usage: webp-guard completion <shell>") {
		t.Fatalf("expected completion help, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected help to keep stderr empty, got %q", stderr.String())
	}
}

func TestRunWithoutArgsPrintsRootHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("expected root help on stdout, got %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "Starting bulk") || strings.Contains(stderr.String(), "Starting bulk") {
		t.Fatalf("expected no-args invocation to show help instead of starting bulk, stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no-args help to keep stderr empty, got %q", stderr.String())
	}
}

func TestVersionCommandPrintsBuildMetadata(t *testing.T) {
	originalVersion := buildVersion
	originalCommit := buildCommit
	originalBuildDate := buildDate
	t.Cleanup(func() {
		buildVersion = originalVersion
		buildCommit = originalCommit
		buildDate = originalBuildDate
	})

	buildVersion = "v1.2.3"
	buildCommit = "abc123def456"
	buildDate = "2026-04-12T07:06:00Z"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{"version"}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d", code)
	}
	for _, needle := range []string{"version: v1.2.3", "commit: abc123def456", "buildDate: 2026-04-12T07:06:00Z"} {
		if !strings.Contains(stdout.String(), needle) {
			t.Fatalf("expected version output to contain %q, got %q", needle, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected version to keep stderr empty, got %q", stderr.String())
	}
}

func TestRootVersionFlagPrintsBuildMetadata(t *testing.T) {
	originalVersion := buildVersion
	t.Cleanup(func() {
		buildVersion = originalVersion
	})

	buildVersion = "v9.9.9"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{"--version"}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d", code)
	}
	if !strings.Contains(stdout.String(), "version: v9.9.9") {
		t.Fatalf("expected root version flag output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected --version to keep stderr empty, got %q", stderr.String())
	}
}

func TestHelpFlagsReturnExitOK(t *testing.T) {
	t.Run("root", func(t *testing.T) {
		for _, args := range [][]string{{"-h"}, {"--help"}} {
			t.Run(strings.Join(args, " "), func(t *testing.T) {
				var stdout bytes.Buffer
				var stderr bytes.Buffer
				code, err := run(context.Background(), args, fakeEncoder{}, &stdout, &stderr)
				if err != nil {
					t.Fatal(err)
				}
				if code != exitOK {
					t.Fatalf("expected exitOK, got %d", code)
				}
				if !strings.Contains(stdout.String(), "Usage:") {
					t.Fatalf("expected root help on stdout, got stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			})
		}
	})

	cases := []struct {
		name string
		args []string
	}{
		{name: "bulk", args: []string{"bulk", "-h"}},
		{name: "scan", args: []string{"scan", "-h"}},
		{name: "verify", args: []string{"verify", "-h"}},
		{name: "resume", args: []string{"resume", "-h"}},
		{name: "plan", args: []string{"plan", "-h"}},
		{name: "publish", args: []string{"publish", "-h"}},
		{name: "verify-delivery", args: []string{"verify-delivery", "-h"}},
		{name: "init", args: []string{"init", "-h"}},
		{name: "doctor", args: []string{"doctor", "-h"}},
		{name: "completion", args: []string{"completion", "-h"}},
		{name: "version", args: []string{"version", "-h"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code, err := run(context.Background(), tc.args, fakeEncoder{}, &stdout, &stderr)
			if err != nil {
				t.Fatal(err)
			}
			if code != exitOK {
				t.Fatalf("expected exitOK, got %d", code)
			}
			if !strings.Contains(stderr.String(), "Usage:") {
				t.Fatalf("expected subcommand help on stderr, got stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestDoctorJSONReportsWarningsAndPasses(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{"doctor", "-json"}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK with warnings only, got %d", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected doctor -json to keep stderr empty, got %q", stderr.String())
	}

	var summary DoctorSummary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("expected JSON doctor summary, got %q: %v", stdout.String(), err)
	}
	if !summary.OK {
		t.Fatalf("expected doctor summary to stay OK with warnings only, got %#v", summary)
	}
	if summary.Warnings == 0 {
		t.Fatalf("expected a warning for missing config, got %#v", summary)
	}
}

func TestDoctorFailsWhenEncoderCheckFails(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{"doctor"}, fakeEncoder{
		check: func() error {
			return errors.New("cwebp missing")
		},
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitConfigError {
		t.Fatalf("expected exitConfigError, got %d", code)
	}
	if !strings.Contains(stdout.String(), "[fail] encoder: cwebp missing") {
		t.Fatalf("expected doctor output to explain the failing encoder check, got %q", stdout.String())
	}
}

func TestDoctorJSONReportsRepresentativeConfigPaths(t *testing.T) {
	root := t.TempDir()
	assetsDir := filepath.Join(root, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := `schema_version = 1

[process]
dir = "./assets"

[verify]
manifest = "./out/conversion-manifest.json"

[publish]
plan = "./out/deploy-plan.dev.json"
`
	if err := os.WriteFile(filepath.Join(root, defaultConfigFileName), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{"doctor", "-json"}, fakeEncoder{}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK with warnings only, got %d", code)
	}

	var summary DoctorSummary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("expected JSON doctor summary, got %q: %v", stdout.String(), err)
	}

	found := false
	for _, check := range summary.Checks {
		if check.Name != "config-paths" {
			continue
		}
		found = true
		if check.Status != doctorStatusWarn {
			t.Fatalf("expected config-paths warning, got %#v", check)
		}
		if !strings.Contains(check.Detail, "process.dir") || !strings.Contains(check.Detail, "verify.manifest") {
			t.Fatalf("expected config-path details to mention representative fields, got %#v", check)
		}
	}
	if !found {
		t.Fatalf("expected config-paths check, got %#v", summary.Checks)
	}
}

func TestDoctorJSONReportsCWebPVersion(t *testing.T) {
	originalLookPath := doctorLookPath
	originalRunCmd := doctorRunCmd
	t.Cleanup(func() {
		doctorLookPath = originalLookPath
		doctorRunCmd = originalRunCmd
	})

	doctorLookPath = func(file string) (string, error) {
		if file != "cwebp" {
			t.Fatalf("unexpected binary lookup %q", file)
		}
		return "/usr/local/bin/cwebp", nil
	}
	doctorRunCmd = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "/usr/local/bin/cwebp" {
			t.Fatalf("unexpected command %q", name)
		}
		if len(args) != 1 || args[0] != "-version" {
			t.Fatalf("unexpected args %v", args)
		}
		return []byte("1.4.0\n"), nil
	}

	root := t.TempDir()
	t.Chdir(root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code, err := run(context.Background(), []string{"doctor", "-json"}, newCWebPEncoder("cwebp"), &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitOK {
		t.Fatalf("expected exitOK, got %d", code)
	}

	var summary DoctorSummary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("expected JSON doctor summary, got %q: %v", stdout.String(), err)
	}

	found := false
	for _, check := range summary.Checks {
		if check.Name != "encoder-version" {
			continue
		}
		found = true
		if check.Status != doctorStatusPass {
			t.Fatalf("expected encoder-version pass, got %#v", check)
		}
		if !strings.Contains(check.Detail, "1.4.0") {
			t.Fatalf("expected version detail, got %#v", check)
		}
	}
	if !found {
		t.Fatalf("expected encoder-version check, got %#v", summary.Checks)
	}
}
