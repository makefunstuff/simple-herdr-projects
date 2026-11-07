package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// spaceStartCmd starts the Assistant of every space, or of the one named.
// A space that already has a live Assistant is left alone.
func spaceStartCmd(raw []string) error {
	a, err := parseArgs(raw, nil, nil)
	if err != nil {
		return err
	}
	if len(a.words) > 1 {
		return usage("space start takes at most one space")
	}
	var names []string
	if len(a.words) == 1 {
		names = a.words
	} else {
		found, err := spaces()
		if err != nil {
			return err
		}
		for _, s := range found {
			names = append(names, s.name)
		}
		slices.Sort(names)
	}
	failed := false
	for _, name := range names {
		if err := startCoordinator(name); err != nil {
			fmt.Fprintf(os.Stderr, "shp: %v\n", err)
			failed = true
		}
	}
	if failed {
		return exitCode(1)
	}
	return nil
}

// startCoordinator opens a space's Assistant in its Herdr space, in the
// Assistant tab, and points it at the space file.
func startCoordinator(name string) error {
	path, head, _, err := loadSpace(name)
	if err != nil {
		return err
	}
	folder := expand(head.text("folder"))
	if !exists(folder) {
		return refuse("space %s: its folder %s is gone", name, tilde(folder))
	}
	workspace := head.text("herdr_space")
	if workspace != "" {
		open, err := openSpace(workspace)
		if err != nil {
			return err
		}
		if open == nil {
			workspace = "" // closed by hand: make or find the one at the folder
		}
	}
	if workspace == "" {
		workspace, err = repoSpace(name, folder)
		if err != nil {
			return err
		}
		if err := recordHerdrSpace(name, workspace); err != nil {
			return err
		}
	}
	alive, err := liveTerminals("herdr.role=coordinator", "herdr.space="+workspace)
	if err != nil {
		return err
	}
	if len(alive) > 0 {
		fmt.Printf("space %s: its Assistant is already live in %s\n", name, alive[0])
		return nil
	}
	argv := head.words("coordinator")
	if len(argv) == 0 {
		cfg, err := config()
		if err != nil {
			return err
		}
		argv = cfg.words("coordinator")
	}
	prompt := guide("assistant", coordinatorGuide)
	if !strings.Contains(prompt, "{{") {
		prompt += fmt.Sprintf("\nYour space is %s at %s; its file is %s.\n", name, tilde(folder), tilde(path))
	}
	prompt = fillGuide(prompt, "{{space}}", name, "{{folder}}", tilde(folder), "{{file}}", tilde(path))
	reply, err := herdrApp("tab.new", map[string]any{
		"name":   "Assistant",
		"space":  workspace,
		"folder": folder,
		"argv":   argv,
		"task":   prompt,
	})
	if err != nil {
		return err
	}
	terminal, _ := reply["terminal"].(string)
	if terminal == "" {
		return refuse("herdr-app made no Assistant terminal for space %s", name)
	}
	if _, err := hc("label", terminal, "herdr.role=coordinator"); err != nil {
		return err
	}
	if err := deliver(terminal, prompt, name); err != nil {
		return err
	}
	fmt.Printf("space %s: its Assistant is in %s\n", name, terminal)
	return nil
}

// recordHerdrSpace saves the Herdr space a space was found in.
func recordHerdrSpace(name, id string) error {
	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	path, head, body, err := loadSpace(name)
	if err != nil {
		return err
	}
	head.set("herdr_space", id)
	return writeText(path, render(head, body))
}
