package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// levels are a finding's levels, most severe first.
var levels = []string{"blocker", "major", "minor"}

// reviewPrompt is the first message a reviewer gets.
func reviewPrompt(t task, spaceHead header, head, report string) string {
	checks := "none"
	if list := spaceHead.list("checks"); len(list) > 0 {
		names := make([]string, len(list))
		for i, argv := range list {
			words, _ := asStrings(argv)
			names[i] = strings.Join(words, " ")
		}
		checks = strings.Join(names, "; ")
	}
	return fmt.Sprintf(
		"You review task %s, %q, read-only: never edit files, commit or push.\n"+
			"Follow ~/.config/herdr-core/templates/reviewer.md.\n"+
			"The task file is %s. Review commit %s in %s: git diff %s...HEAD. Checks: %s.\n"+
			"Write your report to %s: first a header of counts,\n"+
			"---\nblocker: <n>\nmajor: <n>\nminor: <n>\n---\n"+
			"then each finding with its level, file, line and what to do. Then stop.",
		t.id, t.head.text("title"), tilde(t.path), head, expand(t.head.text("worktree")),
		spaceHead.text("base"), checks, report)
}

func reviewCmd(raw []string) error {
	a, err := parseArgs(raw, nil, []string{"again"})
	if err != nil {
		return err
	}
	if len(a.words) != 1 {
		return usage("review takes one task id")
	}
	id := a.words[0]
	t, err := loadTask(id)
	if err != nil {
		return err
	}
	if err := reviewable(t); err != nil {
		return err
	}
	cfg, err := config()
	if err != nil {
		return err
	}
	done, _ := t.head.number("review")
	limit, _ := cfg.number("rounds")
	if done >= limit && !a.bools["again"] {
		return refuse("%s has had %d review rounds, the limit; ask the person: only they pass --again", id, done)
	}
	round := done + 1
	_, spaceHead, _, err := loadSpace(t.space)
	if err != nil {
		return err
	}
	worktree := expand(t.head.text("worktree"))
	if err := clean(worktree); err != nil {
		return err
	}
	head, err := gitHead(worktree)
	if err != nil {
		return err
	}
	report := filepath.Join(os.TempDir(), fmt.Sprintf("shp-review-%s-%d.md", id, round))
	if err := os.Remove(report); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return wrap(report, err)
	}

	// The last round's reviewer stays with its report until this one starts.
	killTerminals(id, "reviewer")
	prompt := reviewPrompt(t, spaceHead, head, report)
	payload := map[string]any{"name": "Review", "folder": worktree, "argv": cfg.words("reviewer"), "task": prompt}
	if workspace := t.head.text("workspace"); workspace != "" {
		payload["space"] = workspace
	}
	reply, err := herdrApp("tab.new", payload)
	if err != nil {
		return err
	}
	terminal, _ := reply["terminal"].(string)
	if terminal == "" {
		return refuse("herdr-app made no reviewer terminal for %s", id)
	}
	if _, err := hc("label", terminal, "herdr.task="+id, "herdr.role=reviewer"); err != nil {
		return err
	}
	if err := deliver(terminal, prompt, id); err != nil {
		return err
	}
	fmt.Printf("%s: round %d is with the reviewer in %s\n", id, round, terminal)
	if err := awaitReport(terminal, report); err != nil {
		return err
	}
	now, err := gitHead(worktree)
	if err != nil {
		return err
	}
	if now != head {
		return refuse("%s moved from %.7s to %.7s during round %d; no round recorded, review again", id, head, now, round)
	}
	return recordRound(id, round, head, report)
}

// awaitReport waits for the reviewer's report however long it takes; it gives
// up only when the reviewer has ended without writing one.
func awaitReport(terminal, report string) error {
	for {
		if isFile(report) {
			return nil
		}
		reply, err := hcJSON("get", terminal)
		if err != nil {
			return err
		}
		if reply["state"] != "live" {
			return refuse("the reviewer in %s ended without writing %s; no round recorded", terminal, report)
		}
		started := time.Now()
		if _, err := hcJSON("wait", terminal, "--timeout", "60"); err != nil {
			return err
		}
		if !isFile(report) && time.Since(started) < 5*time.Second {
			time.Sleep(5 * time.Second) // an idle reviewer ends a wait at once
		}
	}
}

func reviewedCmd(raw []string) error {
	a, err := parseArgs(raw, []string{"report"}, nil)
	if err != nil {
		return err
	}
	report, ok := a.value("report")
	if len(a.words) != 1 || !ok {
		return usage("reviewed takes one task id and --report FILE")
	}
	id := a.words[0]
	t, err := loadTask(id)
	if err != nil {
		return err
	}
	if err := reviewable(t); err != nil {
		return err
	}
	worktree := expand(t.head.text("worktree"))
	if err := clean(worktree); err != nil {
		return err
	}
	head, err := gitHead(worktree)
	if err != nil {
		return err
	}
	done, _ := t.head.number("review")
	return recordRound(id, done+1, head, expand(report))
}

// reviewable refuses a task that has no work under way to review.
func reviewable(t task) error {
	if status := t.head.text("status"); status != "working" {
		return refuse("%s is %s; only working tasks are reviewed", t.id, status)
	}
	return nil
}

// recordRound copies a report beside its task and records the round's counts
// and the commit it reviewed.
func recordRound(id string, round int, head, report string) error {
	counts, text, err := reportCounts(report)
	if err != nil {
		return err
	}
	cfg, err := config()
	if err != nil {
		return err
	}
	blocking := 0
	for i := range slices.Index(levels, cfg.text("must_fix")) + 1 {
		blocking += counts[i]
	}
	kept := ""
	_, err = update(id, func(t *task) error {
		kept = filepath.Join(filepath.Dir(t.path), fmt.Sprintf("%s.review-%d.md", id, round))
		t.head.set("review", json.Number(strconv.Itoa(round)))
		t.head.set("reviewed_commit", head)
		t.head.set("blocking", json.Number(strconv.Itoa(blocking)))
		return writeText(kept, text)
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s: round %d: %d blocker, %d major, %d minor; %d to fix (%s)\n",
		id, round, counts[0], counts[1], counts[2], blocking, tilde(kept))
	return nil
}

// reportCounts reads a report's blocker, major and minor counts and its text.
func reportCounts(report string) ([]int, string, error) {
	text, err := readText(report)
	if err != nil {
		return nil, "", err
	}
	noCounts := refuse("%s has no header of blocker, major and minor counts; no round recorded "+
		"(add one, then shp reviewed --report it)", tilde(report))
	head, _, err := parseFile(report)
	if err != nil {
		return nil, "", noCounts
	}
	counts := make([]int, len(levels))
	for i, level := range levels {
		n, ok := head.number(level)
		if !ok || n < 0 {
			return nil, "", noCounts
		}
		counts[i] = n
	}
	return counts, text, nil
}

func readyCmd(raw []string) error {
	a, err := parseArgs(raw, []string{"summary", "proof"}, nil)
	if err != nil {
		return err
	}
	if len(a.words) != 1 {
		return usage("ready takes one task id")
	}
	t, err := update(a.words[0], func(t *task) error {
		if status := t.head.text("status"); status != "working" {
			return refuse("%s is %s; only working tasks get ready", t.id, status)
		}
		if err := reviewedNow(*t); err != nil {
			return err
		}
		t.head.set("status", "ready")
		if summary, ok := a.value("summary"); ok {
			t.head.set("summary", summary)
		}
		if proof, ok := a.value("proof"); ok {
			t.head.set("proof", proof)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Println(taskLine(t.head))
	return nil
}

// reviewedNow refuses a task whose last round is missing, stale or left
// findings to fix.
func reviewedNow(t task) error {
	round, _ := t.head.number("review")
	if round == 0 {
		return refuse("%s has no review round; run shp review %s", t.id, t.id)
	}
	head, err := gitHead(expand(t.head.text("worktree")))
	if err != nil {
		return err
	}
	if head != t.head.text("reviewed_commit") {
		return refuse("%s has commits after round %d; review again", t.id, round)
	}
	if blocking, _ := t.head.number("blocking"); blocking > 0 {
		return refuse("round %d of %s left %d findings to fix; fix them, commit and review again", round, t.id, blocking)
	}
	return nil
}

// gitHead is the commit a checkout is on.
func gitHead(dir string) (string, error) {
	out, err := git("-C", dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// clean refuses a checkout with changes git would commit.
func clean(dir string) error {
	out, err := git("-C", dir, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "" {
		return refuse("%s has uncommitted changes", tilde(dir))
	}
	return nil
}

// killTerminals ends a task's live terminals, those of one role when given;
// one that will not end is reported and left.
func killTerminals(id, role string) {
	labels := []string{"ls", "--label", "herdr.task=" + id}
	if role != "" {
		labels = append(labels, "--label", "herdr.role="+role)
	}
	found, err := hcJSON(labels...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shp: %v\n", err)
		return
	}
	terminals, _ := found["terminals"].([]any)
	for _, item := range terminals {
		terminal, _ := item.(map[string]any)
		address, _ := terminal["address"].(string)
		if terminal["state"] != "live" || address == "" {
			continue
		}
		if _, err := hc("kill", address); err != nil {
			fmt.Fprintf(os.Stderr, "shp: %v\n", err)
		}
	}
}
