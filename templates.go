package main

import (
	_ "embed"
	"path/filepath"
	"strings"
)

// The agent guides shp hands out. They are files so a person can read them,
// and embedded so shp needs nothing outside its binary.

//go:embed templates/worker.md
var workerGuide string

//go:embed templates/reviewer.md
var reviewerGuide string

//go:embed templates/coordinator.md
var coordinatorGuide string

// guide returns a role's guide: the file a person edits in hc's template folder,
// else the guide embedded in shp. The frontmatter an hc template carries is not
// part of the guide.
func guide(role, embedded string) string {
	text, err := readText(filepath.Join(homeDir(), ".config", "herdr-core", "templates", role+".md"))
	if err != nil || strings.TrimSpace(text) == "" {
		return embedded
	}
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if end := strings.Index(rest, "\n---\n"); end >= 0 {
			return rest[end+len("\n---\n"):]
		}
	}
	return text
}

// fillGuide replaces a guide's named placeholders with one task's or space's values.
func fillGuide(text string, pairs ...string) string {
	return strings.NewReplacer(pairs...).Replace(text)
}
