package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectConfigSelectionRecognizesNoConfigEqualsBool(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{
			name: "true",
			args: []string{"bulk", "--no-config=true"},
			want: true,
		},
		{
			name: "false",
			args: []string{"bulk", "--no-config=false"},
			want: false,
		},
		{
			name: "single-dash",
			args: []string{"bulk", "-no-config=true"},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			selection, err := detectConfigSelection(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if selection.NoConfig != tc.want {
				t.Fatalf("expected NoConfig=%t, got %#v", tc.want, selection)
			}
		})
	}
}

func TestDetectConfigSelectionRejectsInvalidNoConfigBool(t *testing.T) {
	_, err := detectConfigSelection([]string{"bulk", "--no-config=maybe"})
	if err == nil {
		t.Fatal("expected invalid bool to fail")
	}
}

func TestLoadRuntimeConfigHonorsNoConfigEqualsTrue(t *testing.T) {
	root := t.TempDir()
	config := `schema_version = 1

[process]
dir = "./assets"
`
	if err := os.WriteFile(filepath.Join(root, defaultConfigFileName), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	runtimeCfg, err := loadRuntimeConfig("bulk", []string{"bulk", "--no-config=true"})
	if err != nil {
		t.Fatal(err)
	}
	if !runtimeCfg.Selection.NoConfig {
		t.Fatalf("expected no-config selection, got %#v", runtimeCfg.Selection)
	}
	if runtimeCfg.BaseDir != "" || runtimeCfg.File.Process.Dir != nil {
		t.Fatalf("expected config file to stay unloaded, got %#v", runtimeCfg)
	}
}

func TestLoadRuntimeConfigLoadsConfigWhenNoConfigEqualsFalse(t *testing.T) {
	root := t.TempDir()
	config := `schema_version = 1

[process]
dir = "./assets"
`
	if err := os.WriteFile(filepath.Join(root, defaultConfigFileName), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	runtimeCfg, err := loadRuntimeConfig("bulk", []string{"bulk", "--no-config=false"})
	if err != nil {
		t.Fatal(err)
	}
	if runtimeCfg.Selection.NoConfig {
		t.Fatalf("expected config loading to remain enabled, got %#v", runtimeCfg.Selection)
	}
	if runtimeCfg.BaseDir == "" || runtimeCfg.File.Process.Dir == nil {
		t.Fatalf("expected config file to load, got %#v", runtimeCfg)
	}
}

func TestLoadRuntimeConfigDoesNotTreatHelpFlagValueAsHelp(t *testing.T) {
	root := t.TempDir()
	config := `schema_version = 1

[process]
dir = "./assets"
`
	if err := os.WriteFile(filepath.Join(root, defaultConfigFileName), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	runtimeCfg, err := loadRuntimeConfig("bulk", []string{"-dir", "help"})
	if err != nil {
		t.Fatal(err)
	}
	if runtimeCfg.BaseDir == "" || runtimeCfg.File.Process.Dir == nil {
		t.Fatalf("expected config file to load when \"help\" is only a flag value, got %#v", runtimeCfg)
	}
}

func TestArgsRequestHelpSkipsConsumedFlagValues(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    bool
	}{
		{
			name:    "plain help",
			command: "bulk",
			args:    []string{"-h"},
			want:    true,
		},
		{
			name:    "value for dir flag",
			command: "bulk",
			args:    []string{"-dir", "-h"},
			want:    false,
		},
		{
			name:    "value for config flag",
			command: "bulk",
			args:    []string{"--config", "--help"},
			want:    false,
		},
		{
			name:    "bulk dry-run remains boolean",
			command: "bulk",
			args:    []string{"-dry-run", "-h"},
			want:    true,
		},
		{
			name:    "publish dry-run consumes value",
			command: "publish",
			args:    []string{"-dry-run", "-h"},
			want:    false,
		},
		{
			name:    "after double dash",
			command: "bulk",
			args:    []string{"--", "-h"},
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := argsRequestHelp(tc.command, tc.args); got != tc.want {
				t.Fatalf("argsRequestHelp(%q) = %t, want %t", tc.args, got, tc.want)
			}
		})
	}
}
