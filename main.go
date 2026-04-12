package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	code, err := run(context.Background(), os.Args[1:], newCWebPEncoder("cwebp"), os.Stdout, os.Stderr)
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		writeLine(os.Stderr, err)
	}
	os.Exit(code)
}

func run(ctx context.Context, args []string, encoder Encoder, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		return runBulkCommand(ctx, args, encoder, stdout, stderr, true)
	}

	switch args[0] {
	case "bulk":
		return runBulkCommand(ctx, args[1:], encoder, stdout, stderr, false)
	case "scan":
		return runScanCommand(ctx, args[1:], encoder, stdout, stderr)
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
	case "help", "-h", "--help":
		printRootUsage(stdout)
		return 0, nil
	default:
		if strings.HasPrefix(args[0], "-") {
			return runBulkCommand(ctx, args, encoder, stdout, stderr, true)
		}
		return exitConfigError, fmt.Errorf("unknown command %q", args[0])
	}
}
