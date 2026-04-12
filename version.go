package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

var (
	buildVersion = "dev"
	buildCommit  = "unknown"
	buildDate    = "unknown"
)

type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

func runVersionCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		printVersionUsage(stderr)
	}

	helpShown, err := parseCommandFlags(fs, args)
	if err != nil {
		return exitConfigError, err
	}
	if helpShown {
		return exitOK, nil
	}
	if fs.NArg() > 0 {
		return exitConfigError, fmt.Errorf("version does not accept positional arguments")
	}
	if err := ctx.Err(); err != nil {
		return exitInterrupted, err
	}

	info := currentBuildInfo()
	writef(stdout, "version: %s\n", info.Version)
	writef(stdout, "commit: %s\n", info.Commit)
	writef(stdout, "buildDate: %s\n", info.BuildDate)
	return exitOK, nil
}

func currentBuildInfo() BuildInfo {
	return BuildInfo{
		Version:   normalizeBuildValue(buildVersion, "dev"),
		Commit:    normalizeBuildValue(buildCommit, "unknown"),
		BuildDate: normalizeBuildValue(buildDate, "unknown"),
	}
}

func normalizeBuildValue(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func printVersionUsage(w io.Writer) {
	writeLine(w, "Usage: webp-guard version")
	writeLine(w)
	writeLine(w, "Print build version information embedded at build time.")
}
