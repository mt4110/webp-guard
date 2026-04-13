package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code, err := run(ctx, os.Args[1:], newCWebPEncoder("cwebp"), os.Stdout, os.Stderr)
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		if errors.Is(err, context.Canceled) {
			writeLine(os.Stderr, "Interrupted; cleaned up temporary files.")
			if code == exitConfigError {
				code = exitInterrupted
			}
		} else {
			writeLine(os.Stderr, err)
		}
	}
	os.Exit(code)
}

func run(ctx context.Context, args []string, encoder Encoder, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		return runHelpCommand(nil, stdout, stderr)
	}

	switch args[0] {
	case "bulk":
		return runBulkCommand(ctx, args[1:], encoder, stdout, stderr, false)
	case "scan":
		return runScanCommand(ctx, args[1:], encoder, stdout, stderr)
	case "version":
		return runVersionCommand(ctx, args[1:], stdout, stderr)
	case "verify":
		return runVerifyCommand(ctx, args[1:], stdout, stderr)
	case "resume":
		return runResumeCommand(ctx, args[1:], encoder, stdout, stderr)
	case "plan":
		return runPlanCommand(ctx, args[1:], stdout, stderr)
	case "publish":
		return runPublishCommand(ctx, args[1:], stdout, stderr)
	case "verify-delivery":
		return runVerifyDeliveryCommand(ctx, args[1:], stdout, stderr)
	case "init":
		return runInitCommand(ctx, args[1:], stdout, stderr)
	case "doctor":
		return runDoctorCommand(ctx, args[1:], encoder, stdout, stderr)
	case "completion":
		return runCompletionCommand(ctx, args[1:], stdout, stderr)
	case "-version", "--version":
		return runVersionCommand(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		return runHelpCommand(args[1:], stdout, stderr)
	default:
		if strings.HasPrefix(args[0], "-") {
			return runBulkCommand(ctx, args, encoder, stdout, stderr, true)
		}
		return exitConfigError, fmt.Errorf("unknown command %q", args[0])
	}
}
