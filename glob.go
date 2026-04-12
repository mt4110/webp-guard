package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type GlobPattern struct {
	Raw   string
	Regex *regexp.Regexp
}

func compileGlobPatterns(values []string) ([]GlobPattern, error) {
	items := splitCommaList(values)
	patterns := make([]GlobPattern, 0, len(items))
	for _, item := range items {
		raw := filepath.ToSlash(strings.TrimSpace(item))
		if raw == "" {
			continue
		}
		regex, err := globToRegex(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", item, err)
		}
		patterns = append(patterns, GlobPattern{Raw: raw, Regex: regex})
	}
	return patterns, nil
}

func matchesAny(patterns []GlobPattern, relativePath string) bool {
	if len(patterns) == 0 {
		return false
	}
	path := filepath.ToSlash(relativePath)
	for _, pattern := range patterns {
		if pattern.Regex.MatchString(path) {
			return true
		}
	}
	return false
}

func splitCommaList(values []string) []string {
	var items []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				items = append(items, trimmed)
			}
		}
	}
	return items
}

func globToRegex(pattern string) (*regexp.Regexp, error) {
	var builder strings.Builder
	builder.WriteString("^")
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				builder.WriteString(".*")
				i += 2
				continue
			}
			builder.WriteString(`[^/]*`)
		case '?':
			builder.WriteString(`[^/]`)
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			builder.WriteByte('\\')
			builder.WriteByte(pattern[i])
		default:
			builder.WriteByte(pattern[i])
		}
		i++
	}
	builder.WriteString("$")
	return regexp.Compile(builder.String())
}
