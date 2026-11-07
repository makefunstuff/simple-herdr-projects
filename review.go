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
			"%s"+
			"The task file is %s. Review commit %s in %s: git diff %s...HEAD. Checks: %s.\n"+
			"Write your report to %s: first a header of counts,\n"+
			"---\nblocker: <n>\nmajor: <n>\nminor: <n>\n---\n"+
			"then each finding with its level, file, line and what to do. Then stop.",
		t.id, t.head.text("title"), reviewerGuide, tilde(t.path), head, expand(t.head.text("worktree")),
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
	if err := needRepo(t.space, spaceHead); err != nil {
		return err
	}
	worktree := expand(t.head.text("worktree"))
	if err := clean(worktree, true); err != nil {
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

	// The last round's reviewer stays with its report until this one is in.
	previous, err := liveTerminals("herdr.task="+id, "herdr.role=reviewer")
	if err != nil {
		return err
	}
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
	kill(previous)
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
	return recordRound(t, round, head, report)
}

// awaitReport waits for the reviewer's report however long it takes; it gives
// up only when the reviewer has ended without writing one.
func awaitReport(terminal, report string) error {
	for !isFile(report) {
		started := time.Now()
		err := reviewerWait(terminal, report)
		if isFile(report) { // written just before the reviewer ended
			return nil
		}
		if err != nil {
			return err
		}
		if time.Since(started) < 5*time.Second {
			time.Sleep(5 * time.Second) // an idle reviewer ends a wait at once
		}
	}
	return nil
}

// reviewerWait waits once on a reviewer, refusing one that has ended.
func reviewerWait(terminal, report string) error {
	reply, err := hcJSON("get", terminal)
	if err != nil {
		return err
	}
	if reply["state"] != "live" {
		return refuse("the reviewer in %s ended without writing %s; no round recorded", terminal, report)
	}
	_, err = hcJSON("wait", terminal, "--timeout", "60")
	return err
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
	if err := clean(worktree, true); err != nil {
		return err
	}
	head, err := gitHead(worktree)
	if err != nil {
		return err
	}
	done, _ := t.head.number("review")
	return recordRound(t, done+1, head, expand(report))
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
func recordRound(t task, round int, head, report string) error {
	counts, text, err := reportCounts(report)
	if err != nil {
		return err
	}
	cfg, err := config()
	if err != nil {
		return err
	}
	atOrAbove := counts[:slices.Index(levels, cfg.text("must_fix"))+1]
	blocking := 0
	for _, n := range atOrAbove {
		blocking += n
	}
	kept := filepath.Join(filepath.Dir(t.path), fmt.Sprintf("%s.review-%d.md", t.id, round))
	_, err = update(t.id, func(cur *task) error {
		if err := reviewable(*cur); err != nil {
			return err
		}
		cur.head.set("review", json.Number(strconv.Itoa(round)))
		cur.head.set("reviewed_commit", head)
		cur.head.set("blocking", json.Number(strconv.Itoa(blocking)))
		return writeText(kept, text)
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s: round %d: %d blocker, %d major, %d minor; %d to fix (%s)\n",
		t.id, round, counts[0], counts[1], counts[2], blocking, tilde(kept))
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
	head, _, err := parseText(report, text)
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
	t, err := loadTask(a.words[0])
	if err != nil {
		return err
	}
	head, err := gitHead(expand(t.head.text("worktree")))
	if err != nil {
		return err
	}
	t, err = update(t.id, func(cur *task) error {
		if status := cur.head.text("status"); status != "working" {
			return refuse("%s is %s; only working tasks get ready", cur.id, status)
		}
		if err := reviewedNow(*cur, head); err != nil {
			return err
		}
		cur.head.set("status", "ready")
		if summary, ok := a.value("summary"); ok {
			cur.head.set("summary", summary)
		}
		if proof, ok := a.value("proof"); ok {
			cur.head.set("proof", proof)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Println(taskLine(t.head))
	return nil
}

// reviewedNow refuses a task on head whose last round is missing, stale or
// left findings to fix.
func reviewedNow(t task, head string) error {
	round, _ := t.head.number("review")
	if round == 0 {
		return refuse("%s has no review round; run shp review %s", t.id, t.id)
	}
	if head != t.head.text("reviewed_commit") {
		return refuse("round %d of %s is stale (reopened or new commits); review again", round, t.id)
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

// clean refuses a checkout with uncommitted changes, counting untracked files
// when asked: they would stay out of a review and block a worktree's removal.
func clean(dir string, untracked bool) error {
	mode := "--untracked-files=no"
	if untracked {
		mode = "--untracked-files=normal"
	}
	out, err := git("-C", dir, "status", "--porcelain", mode)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	if untracked {
		return refuse("%s has uncommitted changes or untracked files", tilde(dir))
	}
	return refuse("%s has uncommitted changes", tilde(dir))
}

// killTerminals ends a task's live terminals.
func killTerminals(id string) {
	alive, err := liveTerminals("herdr.task=" + id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shp: %v\n", err)
		return
	}
	kill(alive)
}

// kill ends terminals; one that will not end is reported and left.
func kill(addresses []string) {
	for _, address := range addresses {
		if _, err := hc("kill", address); err != nil {
			fmt.Fprintf(os.Stderr, "shp: %v\n", err)
		}
	}
}
