package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "herdr-core", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("worker", "---\nname: worker\ncommand: claude\n---\nWork {{id}} in {{space}}.\n")
	got := fillGuide(guide("worker", workerGuide), "{{id}}", "APP-1", "{{space}}", "work")
	if got != "Work APP-1 in work.\n" {
		t.Errorf("the edited worker guide is %q", got)
	}
	if err := os.Remove(filepath.Join(dir, "worker.md")); err != nil {
		t.Fatal(err)
	}
	if guide("worker", workerGuide) != workerGuide {
		t.Error("a missing guide file did not fall back to the embedded guide")
	}

	write("assistant", "---\nname: assistant\ncommand: claude\n---\nGeneric guide.\n")
	prompt := coordinatorPrompt("home", header{fields: []field{{"folder", "~"}}}, "~/.herdr/simple/spaces/home.md")
	for _, want := range []string{"Generic guide.", "Your space is home at ~; its file is ~/.herdr/simple/spaces/home.md."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the Assistant prompt lacks %q:\n%s", want, prompt)
		}
	}
}
