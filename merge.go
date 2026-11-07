package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func acceptCmd(raw []string) error {
	a, err := parseArgs(raw, nil, []string{"yes"})
	if err != nil {
		return err
	}
	if len(a.words) != 1 {
		return usage("accept takes one task id")
	}
	id := a.words[0]
	t, err := loadTask(id)
	if err != nil {
		return err
	}
	if status := t.head.text("status"); status != "ready" {
		return refuse("%s is %s; only ready tasks are accepted", id, status)
	}
	_, spaceHead, _, err := loadSpace(t.space)
	if err != nil {
		return err
	}
	worktree := expand(t.head.text("worktree"))
	head, err := gitHead(worktree)
	if err != nil {
		return err
	}
	if head != t.head.text("reviewed_commit") {
		return backToWork(id)
	}
	if err := reviewedNow(t, head); err != nil {
		return err
	}
	if err := clean(worktree, true); err != nil {
		return fmt.Errorf("%w; %s stays ready", err, id)
	}
	other, err := mergingIn(t.space)
	if err != nil {
		return err
	}
	if other != "" {
		return refuse("%s is merging in %s; one accept per space at a time", other, t.space)
	}
	if !a.bools["yes"] && !confirm(fmt.Sprintf("Merge %s, %q, into %s? [y/N] ", id, t.head.text("title"), spaceHead.text("base"))) {
		return refuse("%s was not accepted", id)
	}
	for _, check := range spaceHead.list("checks") {
		argv, _ := asStrings(check)
		if len(argv) == 0 {
			continue
		}
		out, errOut, err := runIn(worktree, argv[0], argv[1:]...)
		if err != nil {
			return refuse("check %s failed; %s stays ready:\n%s", strings.Join(argv, " "), id, strings.TrimSpace(out+errOut))
		}
	}
	now, err := gitHead(worktree)
	if err != nil {
		return err
	}
	if now != head {
		return backToWork(id)
	}
	if spaceHead.text("merge") == "pr" {
		return openPR(t, spaceHead)
	}
	merged, err := mergeLocal(t, spaceHead)
	if err != nil {
		return err
	}
	cleanUp(merged, expand(spaceHead.text("folder")))
	return nil
}

// backToWork returns a ready task with commits after its last round to working.
func backToWork(id string) error {
	_, err := update(id, func(cur *task) error { return move(cur, "ready", "working") })
	if err != nil {
		return err
	}
	return refuse("%s has commits after its last review round; it is working again: review, then ready", id)
}

// confirm asks a yes or no question on the terminal.
func confirm(question string) bool {
	fmt.Print(question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

// move moves a task from one status to another, refusing one that has moved on.
func move(t *task, from, to string) error {
	if status := t.head.text("status"); status != from {
		return refuse("%s is %s now, not %s", t.id, status, from)
	}
	t.head.set("status", to)
	return nil
}

// mergingIn is a task of the space whose pull request is merging, if any; a
// task file that will not read could be one, so it refuses.
func mergingIn(spaceName string) (string, error) {
	tasks, problems := allTasks()
	if len(problems) > 0 {
		return "", fmt.Errorf("%w; fix it before accepting", problems[0])
	}
	for _, t := range tasks {
		if t.space == spaceName && t.head.text("status") == "merging" {
			return t.id, nil
		}
	}
	return "", nil
}

// mergeLocal merges a task's reviewed commit into the base in the repo's own
// checkout and returns the task, done. It holds the store's lock throughout
// and checks the task is still ready on that commit, so two accepts never
// merge it twice.
func mergeLocal(accepted task, spaceHead header) (task, error) {
	repo := expand(spaceHead.text("folder"))
	base := spaceHead.text("base")
	reviewed := accepted.head.text("reviewed_commit")
	unlock, err := lock()
	if err != nil {
		return task{}, err
	}
	defer unlock()
	t, err := loadTask(accepted.id)
	if err != nil {
		return task{}, err
	}
	if err := stillAccepted(t, reviewed); err != nil {
		return task{}, err
	}
	if err := clean(repo, false); err != nil {
		return task{}, fmt.Errorf("%w; commit or stash them, then accept %s again", err, t.id)
	}
	current, err := git("-C", repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return task{}, err
	}
	if current = strings.TrimSpace(current); current != base {
		return task{}, refuse("%s is on %s, not %s; check out %s, then accept %s again", tilde(repo), current, base, base, t.id)
	}
	message := fmt.Sprintf("Merge %s: %s", t.id, t.head.text("title"))
	if _, errOut, err := run("git", "-C", repo, "merge", "--no-ff", "-m", message, reviewed); err != nil {
		conflicts, _ := git("-C", repo, "diff", "--name-only", "--diff-filter=U")
		_, _, _ = run("git", "-C", repo, "merge", "--abort")
		if files := strings.Fields(conflicts); len(files) > 0 {
			return task{}, refuse("%s conflicts with %s in %s; it stays ready: merge %s into its branch, commit and review again",
				t.id, base, strings.Join(files, ", "), base)
		}
		return task{}, refuse("git merge: %s; %s stays ready", reason(errOut, ""), t.id)
	}
	merged, err := gitHead(repo)
	if err != nil {
		return task{}, err
	}
	t.head.set("status", "done")
	t.head.set("merged", merged)
	if err := writeText(t.path, render(t.head, t.body)); err != nil {
		return task{}, err
	}
	fmt.Printf("%s: merged into %s as %.7s\n", t.id, base, merged)
	return t, nil
}

// stillAccepted refuses a task that was reopened or reviewed again while it
// was being accepted.
func stillAccepted(t task, reviewed string) error {
	if status := t.head.text("status"); status != "ready" {
		return refuse("%s changed while it was being accepted (now %s); accept it again", t.id, status)
	}
	if t.head.text("reviewed_commit") != reviewed {
		return refuse("%s was reviewed again while it was being accepted; accept it again", t.id)
	}
	return nil
}

// cleanUp ends a merged task's terminals, removes its worktree and branch,
// then closes its space; whatever will not go is kept and named. The space
// goes last: an accept run from its Terminal tab ends with it.
func cleanUp(t task, repo string) {
	killTerminals(t.id)
	defer closeSpace(t)
	// A branch on just the reviewed commit is merged, even by a squash git
	// cannot see; one with newer commits goes only if git agrees.
	deleteFlag := "-d"
	if head, err := git("-C", repo, "rev-parse", "refs/heads/"+t.head.text("branch")); err == nil &&
		strings.TrimSpace(head) == t.head.text("reviewed_commit") {
		deleteFlag = "-D"
	}
	worktree := expand(t.head.text("worktree"))
	if exists(worktree) {
		if _, err := git("-C", repo, "worktree", "remove", worktree); err != nil {
			fmt.Fprintf(os.Stderr, "shp: %v; kept %s and its branch\n", err, tilde(worktree))
			return
		}
	}
	if _, err := git("-C", repo, "branch", deleteFlag, t.head.text("branch")); err != nil {
		fmt.Fprintf(os.Stderr, "shp: %v; kept branch %s\n", err, t.head.text("branch"))
	}
}

// openPR pushes the reviewed commit, opens the task's pull request or reuses
// an open one, and sets it merging; GitHub merges it on green where the repo
// allows, else tick does.
func openPR(t task, spaceHead header) error {
	worktree := expand(t.head.text("worktree"))
	branch := t.head.text("branch")
	reviewed := t.head.text("reviewed_commit")
	if _, err := git("-C", worktree, "push", "origin", reviewed+":refs/heads/"+branch); err != nil {
		return err
	}
	url := ""
	if out, _, err := runIn(worktree, "gh", "pr", "view", branch, "--json", "url,state"); err == nil {
		var pr struct{ URL, State string }
		if json.Unmarshal([]byte(out), &pr) == nil && pr.State == "OPEN" {
			url = pr.URL
		}
	}
	if url == "" {
		body := t.head.text("summary")
		if proof := t.head.text("proof"); proof != "" {
			body += "\n\nProof: " + proof
		}
		body = strings.TrimSpace(body + "\n\nTask: " + t.id)
		out, errOut, err := runIn(worktree, "gh", "pr", "create", "--base", spaceHead.text("base"), "--head", branch,
			"--title", t.id+" "+t.head.text("title"), "--body", body)
		if err != nil {
			return refuse("gh pr create: %s; %s stays ready", reason(errOut, out), t.id)
		}
		url = lastLine(out)
	}
	t, err := update(t.id, func(cur *task) error {
		if err := stillAccepted(*cur, reviewed); err != nil {
			return err
		}
		cur.head.set("status", "merging")
		cur.head.set("pr", url)
		return nil
	})
	if err != nil {
		return err
	}
	if err := openPRTab(t); err != nil {
		fmt.Fprintf(os.Stderr, "shp: %v; %s has no PR tab\n", err, t.id)
	}
	if _, _, err := runIn(worktree, "gh", "pr", "merge", "--auto", "--squash", url); err != nil {
		fmt.Printf("%s: merging through %s; tick merges it once its checks pass\n", t.id, url)
		return nil
	}
	fmt.Printf("%s: merging through %s once its checks pass\n", t.id, url)
	return nil
}

// followMerges moves each merging task on with its pull request.
func followMerges(tasks []task) []error {
	var problems []error
	for _, t := range tasks {
		if t.head.text("status") != "merging" {
			continue
		}
		if err := followMerge(t); err != nil {
			fmt.Fprintf(os.Stderr, "shp: %v\n", err)
			problems = append(problems, err)
		}
	}
	return problems
}

// followMerge reads a merging task's pull request: merged is done, closed or
// conflicting is ready again, and green is merged now.
func followMerge(t task) error {
	_, spaceHead, _, err := loadSpace(t.space)
	if err != nil {
		return err
	}
	repo := expand(spaceHead.text("folder"))
	url := t.head.text("pr")
	out, errOut, err := runIn(repo, "gh", "pr", "view", url, "--json", "state,mergeable,mergeStateStatus,mergeCommit")
	if err != nil {
		return refuse("%s: gh pr view: %s", t.id, reason(errOut, out))
	}
	var pr struct {
		State, Mergeable, MergeStateStatus string
		MergeCommit                        struct{ Oid string }
	}
	if err := json.Unmarshal([]byte(out), &pr); err != nil {
		return refuse("%s: gh pr view printed no JSON", t.id)
	}
	switch {
	case pr.State == "MERGED":
		return finishPR(t, repo, pr.MergeCommit.Oid)
	case pr.State == "CLOSED":
		return backToReady(t.id, "its pull request was closed; accept again for a new one")
	case pr.Mergeable == "CONFLICTING":
		base := spaceHead.text("base")
		return backToReady(t.id, "its pull request conflicts with "+base+
			"; reopen, merge "+base+" into its branch, commit and review again")
	case pr.MergeStateStatus == "CLEAN":
		if _, errOut, err := runIn(repo, "gh", "pr", "merge", "--squash", url); err != nil {
			return refuse("%s: gh pr merge: %s", t.id, reason(errOut, ""))
		}
	}
	return nil
}

// finishPR marks a task whose pull request merged as done and cleans up after it.
func finishPR(t task, repo, merged string) error {
	t, err := update(t.id, func(cur *task) error {
		if err := move(cur, "merging", "done"); err != nil {
			return err
		}
		if merged != "" { // GitHub may not name the commit yet
			cur.head.set("merged", merged)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s: merged as %.7s\n", t.id, merged)
	cleanUp(t, repo)
	return nil
}

// backToReady returns a merging task to ready, saying why in its step.
func backToReady(id, why string) error {
	_, err := update(id, func(cur *task) error {
		if err := move(cur, "merging", "ready"); err != nil {
			return err
		}
		cur.head.set("step", why)
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s: ready again: %s\n", id, why)
	return nil
}

func doneCmd(raw []string) error {
	a, err := parseArgs(raw, nil, nil)
	if err != nil {
		return err
	}
	if len(a.words) != 1 {
		return usage("done takes one task id")
	}
	id := a.words[0]
	t, err := update(id, func(cur *task) error {
		switch status := cur.head.text("status"); status {
		case "done":
			return refuse("%s is done already", id)
		case "merging":
			return refuse("%s is merging; merge or close its pull request first", id)
		}
		cur.head.set("status", "done")
		return nil
	})
	if err != nil {
		return err
	}
	killTerminals(id)
	// Printed first: closing the space may end the shell this runs in.
	fmt.Printf("%s: done; its branch and worktree are kept\n", id)
	closeSpace(t)
	return nil
}

func reopenCmd(raw []string) error {
	a, err := parseArgs(raw, nil, nil)
	if err != nil {
		return err
	}
	if len(a.words) != 1 {
		return usage("reopen takes one task id")
	}
	t, err := loadTask(a.words[0])
	if err != nil {
		return err
	}
	switch status := t.head.text("status"); status {
	case "ready":
		return reopenReady(t)
	case "done":
		return reopenDone(t)
	default:
		return refuse("%s is %s; only ready or done tasks reopen", t.id, status)
	}
}

// reopenReady sends a ready task back to its worker; its round no longer counts.
func reopenReady(t task) error {
	if err := clean(expand(t.head.text("worktree")), true); err != nil {
		return fmt.Errorf("%w; commit or drop them first", err)
	}
	t, err := update(t.id, func(cur *task) error {
		if err := move(cur, "ready", "working"); err != nil {
			return err
		}
		cur.head.set("reviewed_commit", nil)
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Println(taskLine(t.head))
	return nil
}

// reopenDone starts a done task again: on its worktree if it is still there,
// on its branch if that is, else on a fresh <branch>-2 (or the next free
// number) from the base.
func reopenDone(t task) error {
	_, spaceHead, _, err := loadSpace(t.space)
	if err != nil {
		return err
	}
	repo := expand(spaceHead.text("folder"))
	branch := t.head.text("branch")
	worktree := t.head.text("worktree")
	if !exists(expand(worktree)) {
		// A worktree folder deleted by hand is still registered until pruned.
		_, _, _ = run("git", "-C", repo, "worktree", "prune")
		if branchExists(repo, branch) {
			if _, err := git("-C", repo, "worktree", "add", expand(worktree), branch); err != nil {
				return err
			}
		} else {
			branch, worktree = freshPlace(repo, t)
		}
	}
	_, err = update(t.id, func(cur *task) error {
		if err := move(cur, "done", "todo"); err != nil {
			return err
		}
		cur.head.set("branch", branch)
		cur.head.set("worktree", worktree)
		cur.head.set("reviewed_commit", nil)
		return nil
	})
	if err != nil {
		return err
	}
	started, err := start(t.id, false)
	if err != nil {
		// A launch that fails leaves the task done, as it was.
		_, _ = update(t.id, func(cur *task) error { return move(cur, "todo", "done") })
		return err
	}
	fmt.Println(taskLine(started.head))
	return nil
}

// freshPlace is a new branch and worktree for a task whose own are gone: its
// branch with -2, or the next number that is free. A trailing -<n> on the
// branch is taken as an earlier reopen's and replaced, so suffixes never stack.
func freshPlace(repo string, t task) (string, string) {
	stem, place := t.head.text("branch"), t.head.text("worktree")
	if cut := strings.LastIndex(stem, "-"); cut >= 0 {
		suffix := stem[cut:]
		if n, err := strconv.Atoi(suffix[1:]); err == nil && n >= 2 && strings.HasSuffix(place, suffix) {
			stem, place = stem[:cut], strings.TrimSuffix(place, suffix)
		}
	}
	for n := 2; ; n++ {
		branch, worktree := fmt.Sprintf("%s-%d", stem, n), fmt.Sprintf("%s-%d", place, n)
		if !branchExists(repo, branch) && !exists(expand(worktree)) {
			return branch, worktree
		}
	}
}
