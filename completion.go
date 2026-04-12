package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

var (
	completionCommands = []string{
		"version",
		"bulk",
		"scan",
		"verify",
		"resume",
		"plan",
		"publish",
		"verify-delivery",
		"init",
		"doctor",
		"completion",
		"help",
	}
	completionShells    = []string{"bash", "zsh", "fish", "powershell", "pwsh"}
	rootCompletionFlags = []string{
		"-dir", "-extensions", "-include", "-exclude", "-include-hidden", "-follow-symlinks",
		"-max-file-size-mb", "-max-pixels", "-max-dimension", "-cpus", "-max-width",
		"-aspect-variants", "-crop-mode", "-focus-x", "-focus-y", "-quality", "-workers",
		"-dry-run", "-out-dir", "-overwrite", "-on-existing", "-report", "-manifest",
		"-resume-from", "-json", "-config", "-no-config", "-h", "--help", "-version", "--version",
	}
	bulkCompletionFlags = rootCompletionFlags
	scanCompletionFlags = []string{
		"-dir", "-extensions", "-include", "-exclude", "-include-hidden", "-follow-symlinks",
		"-max-file-size-mb", "-max-pixels", "-max-dimension", "-cpus", "-report", "-json",
		"-config", "-no-config", "-h", "--help",
	}
	verifyCompletionFlags = []string{
		"-dir", "-manifest", "-report", "-max-width", "-cpus", "-json",
		"-config", "-no-config", "-h", "--help",
	}
	planCompletionFlags = []string{
		"-conversion-manifest", "-release-manifest", "-deploy-plan", "-env", "-base-url",
		"-origin-provider", "-origin-root", "-origin-prefix", "-cdn-provider",
		"-immutable-prefix", "-mutable-prefix", "-verify-sample", "-json",
		"-config", "-no-config", "-h", "--help",
	}
	publishCompletionFlags = []string{
		"-plan", "-dry-run", "-json", "-config", "-no-config", "-h", "--help",
	}
	verifyDeliveryCompletionFlags = []string{
		"-plan", "-json", "-config", "-no-config", "-h", "--help",
	}
	initCompletionFlags    = []string{"-path", "-force", "-h", "--help"}
	doctorCompletionFlags  = []string{"-json", "-config", "-no-config", "-h", "--help"}
	completionCommandFlags = []string{"-shell", "-h", "--help"}
	versionCommandFlags    = []string{"-h", "--help"}
)

func runCompletionCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var shell string
	fs.StringVar(&shell, "shell", "", "Shell name: bash, zsh, fish, powershell")
	fs.Usage = func() {
		printCompletionUsage(stderr)
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

	switch fs.NArg() {
	case 0:
	case 1:
		if shell != "" {
			return exitConfigError, fmt.Errorf("shell cannot be provided both as a positional argument and with -shell")
		}
		shell = fs.Arg(0)
	default:
		return exitConfigError, fmt.Errorf("completion accepts a single shell name")
	}

	script, err := renderCompletionScript(shell)
	if err != nil {
		return exitConfigError, err
	}
	writef(stdout, "%s", script)
	return exitOK, nil
}

func renderCompletionScript(shell string) (string, error) {
	switch normalizeCompletionShell(shell) {
	case "bash":
		return renderBashCompletionScript(), nil
	case "zsh":
		return renderZshCompletionScript(), nil
	case "fish":
		return renderFishCompletionScript(), nil
	case "powershell":
		return renderPowerShellCompletionScript(), nil
	default:
		return "", fmt.Errorf("unsupported shell %q (expected bash, zsh, fish, powershell)", shell)
	}
}

func normalizeCompletionShell(shell string) string {
	value := strings.ToLower(strings.TrimSpace(shell))
	switch value {
	case "pwsh":
		return "powershell"
	default:
		return value
	}
}

func completionFlagsForCommand(command string) []string {
	switch command {
	case "bulk", "resume":
		return bulkCompletionFlags
	case "scan":
		return scanCompletionFlags
	case "verify":
		return verifyCompletionFlags
	case "plan":
		return planCompletionFlags
	case "publish":
		return publishCompletionFlags
	case "verify-delivery":
		return verifyDeliveryCompletionFlags
	case "init":
		return initCompletionFlags
	case "doctor":
		return doctorCompletionFlags
	case "version":
		return versionCommandFlags
	case "completion":
		return completionCommandFlags
	default:
		return nil
	}
}

func renderBashCompletionScript() string {
	return fmt.Sprintf(`# bash completion for webp-guard
_webp_guard()
{
	local cur prev command flags
	COMPREPLY=()
	cur="${COMP_WORDS[COMP_CWORD]}"
	prev=""
	if (( COMP_CWORD > 0 )); then
		prev="${COMP_WORDS[COMP_CWORD-1]}"
	fi

	command=""
	local i
	for (( i=1; i<COMP_CWORD; i++ )); do
		case "${COMP_WORDS[i]}" in
			%s)
				command="${COMP_WORDS[i]}"
				break
				;;
		esac
	done

	case "$prev" in
		-dir|-out-dir|-origin-root)
			COMPREPLY=( $(compgen -d -- "$cur") )
			return 0
			;;
		-report|-manifest|-resume-from|-conversion-manifest|-release-manifest|-deploy-plan|-plan|-config|-path)
			COMPREPLY=( $(compgen -f -- "$cur") )
			return 0
			;;
		-crop-mode)
			COMPREPLY=( $(compgen -W "safe focus" -- "$cur") )
			return 0
			;;
		-on-existing)
			COMPREPLY=( $(compgen -W "skip overwrite fail" -- "$cur") )
			return 0
			;;
		-origin-provider)
			COMPREPLY=( $(compgen -W "local" -- "$cur") )
			return 0
			;;
		-cdn-provider)
			COMPREPLY=( $(compgen -W "noop" -- "$cur") )
			return 0
			;;
		-shell)
			COMPREPLY=( $(compgen -W "%s" -- "$cur") )
			return 0
			;;
		-dry-run)
			if [[ "$command" == "publish" ]]; then
				COMPREPLY=( $(compgen -W "off plan verify" -- "$cur") )
			fi
			return 0
			;;
	esac

	if [[ "$command" == "help" ]]; then
		COMPREPLY=( $(compgen -W "%s" -- "$cur") )
		return 0
	fi

	if [[ -z "$command" ]]; then
		if [[ "$cur" == -* ]]; then
			COMPREPLY=( $(compgen -W "%s" -- "$cur") )
		else
			COMPREPLY=( $(compgen -W "%s" -- "$cur") )
		fi
		return 0
	fi

	if [[ "$command" == "completion" && "$cur" != -* ]]; then
		COMPREPLY=( $(compgen -W "%s" -- "$cur") )
		return 0
	fi

	case "$command" in
		bulk|resume)
			flags="%s"
			;;
		scan)
			flags="%s"
			;;
		verify)
			flags="%s"
			;;
		plan)
			flags="%s"
			;;
		publish)
			flags="%s"
			;;
		verify-delivery)
			flags="%s"
			;;
		init)
			flags="%s"
			;;
		doctor)
			flags="%s"
			;;
		version)
			flags="%s"
			;;
		completion)
			flags="%s"
			;;
	esac

	if [[ "$cur" == -* ]]; then
		COMPREPLY=( $(compgen -W "$flags" -- "$cur") )
	fi
	}

complete -F _webp_guard webp-guard
`,
		bashCasePattern(completionCommands),
		completionWords(completionShells),
		completionWords(completionCommands),
		completionWords(rootCompletionFlags),
		completionWords(completionCommands),
		completionWords(completionShells),
		completionWords(bulkCompletionFlags),
		completionWords(scanCompletionFlags),
		completionWords(verifyCompletionFlags),
		completionWords(planCompletionFlags),
		completionWords(publishCompletionFlags),
		completionWords(verifyDeliveryCompletionFlags),
		completionWords(initCompletionFlags),
		completionWords(doctorCompletionFlags),
		completionWords(versionCommandFlags),
		completionWords(completionCommandFlags),
	)
}

func renderZshCompletionScript() string {
	return fmt.Sprintf(`#compdef webp-guard
autoload -U +X bashcompinit && bashcompinit
%s`, renderBashCompletionScript())
}

func renderFishCompletionScript() string {
	var builder strings.Builder

	builder.WriteString("# fish completion for webp-guard\n")
	builder.WriteString("function __webp_guard_using_subcommand\n")
	builder.WriteString("    set -l words (commandline -opc)\n")
	builder.WriteString("    contains -- $argv[1] $words\n")
	builder.WriteString("end\n\n")
	builder.WriteString("function __webp_guard_prev_arg_in\n")
	builder.WriteString("    set -l words (commandline -opc)\n")
	builder.WriteString("    if test (count $words) -eq 0\n")
	builder.WriteString("        return 1\n")
	builder.WriteString("    end\n")
	builder.WriteString("    set -l prev $words[-1]\n")
	builder.WriteString("    contains -- $prev $argv\n")
	builder.WriteString("end\n\n")
	builder.WriteString("complete -c webp-guard -f\n")

	descriptions := map[string]string{
		"version":         "Show build version information",
		"bulk":            "Convert images to .webp outputs",
		"scan":            "Run safety checks without writing outputs",
		"verify":          "Verify conversion manifest entries",
		"resume":          "Resume a previous bulk run",
		"plan":            "Generate release and deploy plan artifacts",
		"publish":         "Upload or preview a deploy plan",
		"verify-delivery": "Run delivery verification checks",
		"init":            "Write a starter webp-guard.toml",
		"doctor":          "Check config and local runtime readiness",
		"completion":      "Generate shell completion scripts",
		"help":            "Show command help",
	}
	for _, command := range completionCommands {
		writef(&builder, "complete -c webp-guard -n '__fish_use_subcommand' -a '%s' -d '%s'\n", command, descriptions[command])
	}
	builder.WriteString("\n")

	for _, command := range completionCommands {
		if command == "help" {
			continue
		}
		for _, flagName := range completionFlagsForCommand(command) {
			writef(&builder, "complete -c webp-guard -n '__webp_guard_using_subcommand %s' -a '%s'\n", command, flagName)
		}
	}
	builder.WriteString("\n")
	builder.WriteString("complete -c webp-guard -n '__webp_guard_using_subcommand help' -a '")
	builder.WriteString(strings.Join(completionCommands, " "))
	builder.WriteString("'\n")
	builder.WriteString("complete -c webp-guard -n '__webp_guard_prev_arg_in completion -shell; and not string match -qr \"^-\" -- (commandline -ct)' -a '")
	builder.WriteString(strings.Join(completionShells, " "))
	builder.WriteString("'\n")
	builder.WriteString("complete -c webp-guard -n '__webp_guard_prev_arg_in -crop-mode' -a 'safe focus'\n")
	builder.WriteString("complete -c webp-guard -n '__webp_guard_prev_arg_in -on-existing' -a 'skip overwrite fail'\n")
	builder.WriteString("complete -c webp-guard -n '__webp_guard_prev_arg_in -origin-provider' -a 'local'\n")
	builder.WriteString("complete -c webp-guard -n '__webp_guard_prev_arg_in -cdn-provider' -a 'noop'\n")
	builder.WriteString("complete -c webp-guard -n '__webp_guard_prev_arg_in -dry-run; and __webp_guard_using_subcommand publish' -a 'off plan verify'\n")
	builder.WriteString("complete -c webp-guard -n '__webp_guard_prev_arg_in -dir -out-dir -origin-root' -a '(__fish_complete_directories)'\n")
	builder.WriteString("complete -c webp-guard -n '__webp_guard_prev_arg_in -report -manifest -resume-from -conversion-manifest -release-manifest -deploy-plan -plan -config -path' -a '(__fish_complete_path)'\n")

	return builder.String()
}

func renderPowerShellCompletionScript() string {
	return fmt.Sprintf(`# PowerShell completion for webp-guard
Register-ArgumentCompleter -CommandName webp-guard -ScriptBlock {
    param($commandName, $wordToComplete, $cursorPosition, $commandAst, $fakeBoundParameters)

    $commands = @(%s)
    $shells = @(%s)
    $flags = @{
        "__root__" = @(%s)
        "bulk" = @(%s)
        "scan" = @(%s)
        "verify" = @(%s)
        "resume" = @(%s)
        "plan" = @(%s)
        "publish" = @(%s)
        "verify-delivery" = @(%s)
        "init" = @(%s)
        "doctor" = @(%s)
        "version" = @(%s)
        "completion" = @(%s)
    }

    $tokens = @()
    foreach ($element in $commandAst.CommandElements) {
        $tokens += $element.Extent.Text
    }

    $currentCommand = $null
    for ($i = 1; $i -lt $tokens.Count; $i++) {
        if ($commands -contains $tokens[$i]) {
            $currentCommand = $tokens[$i]
            break
        }
    }

    $previous = ""
    if ($tokens.Count -ge 2) {
        $previous = $tokens[$tokens.Count - 2]
    }

    $candidates = @()
    switch ($previous) {
        "-shell" { $candidates = $shells; break }
        "-crop-mode" { $candidates = @("safe", "focus"); break }
        "-on-existing" { $candidates = @("skip", "overwrite", "fail"); break }
        "-origin-provider" { $candidates = @("local"); break }
        "-cdn-provider" { $candidates = @("noop"); break }
        "-dry-run" {
            if ($currentCommand -eq "publish") {
                $candidates = @("off", "plan", "verify")
            }
            break
        }
    }

    if ($candidates.Count -eq 0) {
        if ($currentCommand -eq "help") {
            $candidates = $commands
        } elseif (-not $currentCommand) {
            if ($wordToComplete -like "-*") {
                $candidates = $flags["__root__"]
            } else {
                $candidates = $commands
            }
        } elseif ($currentCommand -eq "completion" -and $wordToComplete -notlike "-*") {
            $candidates = $shells
        } elseif ($flags.ContainsKey($currentCommand) -and $wordToComplete -like "-*") {
            $candidates = $flags[$currentCommand]
        }
    }

    foreach ($candidate in $candidates) {
        if ($candidate -like "$wordToComplete*") {
            [System.Management.Automation.CompletionResult]::new($candidate, $candidate, "ParameterValue", $candidate)
        }
    }
}
`,
		powerShellQuoted(completionCommands),
		powerShellQuoted(completionShells),
		powerShellQuoted(rootCompletionFlags),
		powerShellQuoted(bulkCompletionFlags),
		powerShellQuoted(scanCompletionFlags),
		powerShellQuoted(verifyCompletionFlags),
		powerShellQuoted(bulkCompletionFlags),
		powerShellQuoted(planCompletionFlags),
		powerShellQuoted(publishCompletionFlags),
		powerShellQuoted(verifyDeliveryCompletionFlags),
		powerShellQuoted(initCompletionFlags),
		powerShellQuoted(doctorCompletionFlags),
		powerShellQuoted(versionCommandFlags),
		powerShellQuoted(completionCommandFlags),
	)
}

func completionWords(items []string) string {
	return strings.Join(items, " ")
}

func bashCasePattern(items []string) string {
	return strings.Join(items, "|")
}

func powerShellQuoted(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, `"`+item+`"`)
	}
	return strings.Join(quoted, ", ")
}
