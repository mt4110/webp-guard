package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Config holds the command line flags and runtime configuration
type Config struct {
	RootDir   string
	Quality   int
	Overwrite bool
	DryRun    bool
}

func main() {
	var config Config
	flag.StringVar(&config.RootDir, "dir", ".", "Root directory to scan for images")
	flag.IntVar(&config.Quality, "quality", 75, "WebP quality (0-100)")
	flag.BoolVar(&config.Overwrite, "overwrite", false, "Overwrite existing .webp files")
	flag.BoolVar(&config.DryRun, "dry-run", false, "Print commands without executing")
	flag.Parse()

	// Validate dependencies
	if _, err := exec.LookPath("cwebp"); err != nil {
		log.Fatal("Error: cwebp is not installed or not in PATH. Please run inside `nix develop` or install libwebp.")
	}

	fmt.Printf("Starting WebP conversion (Quality: %d, Dir: %s)\n", config.Quality, config.RootDir)

	err := filepath.WalkDir(config.RootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}

		if strings.ToLower(filepath.Ext(path)) != ".png" {
			return nil
		}

		return processFile(path, config)
	})

	if err != nil {
		log.Fatalf("Error walking directory: %v", err)
	}

	fmt.Println("Done.")
}

func processFile(pngPath string, config Config) error {
	webpPath := strings.TrimSuffix(pngPath, filepath.Ext(pngPath)) + ".webp"

	// Check if webp exists
	if !config.Overwrite {
		if _, err := os.Stat(webpPath); err == nil {
			// File exists and overwrite is false
			return nil
		}
	}

	info, err := os.Stat(pngPath)
	if err != nil {
		return fmt.Errorf("stat error: %w", err)
	}
	originalSize := info.Size()

	if config.DryRun {
		fmt.Printf("[DryRun] Convert: %s -> %s\n", pngPath, webpPath)
		return nil
	}

	fmt.Printf("Converting: %s ... ", pngPath)

	cmd := exec.Command("cwebp", "-q", fmt.Sprintf("%d", config.Quality), pngPath, "-o", webpPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("FAILED\nOutput: %s\nError: %v\n", string(output), err)
		return nil // Continue scanning other files
	}

	// Safe-Guard: Check size
	webpInfo, err := os.Stat(webpPath)
	if err != nil {
		fmt.Printf("Error checking result size: %v\n", err)
		return nil
	}
	
	webpSize := webpInfo.Size()
	
	if webpSize >= originalSize {
		// Degraded (larger file), remove it
		fmt.Printf("DISCARDED (Larger than original: %d vs %d)\n", webpSize, originalSize)
		_ = os.Remove(webpPath)
	} else {
		saved := originalSize - webpSize
		ratio := float64(saved) / float64(originalSize) * 100
		fmt.Printf("OK (Saved %.1f%%)\n", ratio)
	}

	return nil
}
