package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// space is one git repo with the prefix its task ids start with.
type space struct {
	name string
	head header
}

var (
	spaceName  = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	prefixForm = regexp.MustCompile(`^[A-Z]{2,5}\d*$`)
	wordChars  = regexp.MustCompile(`[A-Za-z0-9]+`)
	notLetters = regexp.MustCompile(`[^A-Za-z]`)
)

func spacePath(name string) string {
	return filepath.Join(store(), "spaces", name+".md")
}

// spaces reads every space file, by name.
func spaces() ([]space, error) {
	dir := filepath.Join(store(), "spaces")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, wrap(dir, err)
	}
	var found []space
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		head, _, err := parseFile(path)
		if err != nil {
			return nil, err
		}
		found = append(found, space{name: strings.TrimSuffix(entry.Name(), ".md"), head: head})
	}
	return found, nil
}

// loadSpace reads one space and checks it names its repo, prefix and base.
func loadSpace(name string) (string, header, string, error) {
	path := spacePath(name)
	if !isFile(path) {
		return "", header{}, "", refuse("there is no space %s", name)
	}
	head, body, err := parseFile(path)
	if err != nil {
		return "", header{}, "", err
	}
	if err := checkFields(path, head, spaceRules); err != nil {
		return "", header{}, "", err
	}
	for _, key := range []string{"folder", "prefix", "base"} {
		if head.text(key) == "" {
			return "", header{}, "", refuse("%s has no %s", tilde(path), key)
		}
	}
	return path, head, body, nil
}

// oneSpace is the space to use when the command did not name one.
func oneSpace() (string, error) {
	found, err := spaces()
	if err != nil {
		return "", err
	}
	if len(found) == 1 {
		return found[0].name, nil
	}
	if len(found) == 0 {
		return "", refuse("there are no spaces yet; shp space add makes one")
	}
	names := make([]string, len(found))
	for i, s := range found {
		names[i] = s.name
	}
	return "", refuse("there are %d spaces; pass --space (%s)", len(names), strings.Join(names, ", "))
}

// initials makes a space's prefix from its name: one letter per word, or four.
func initials(name string) string {
	words := wordChars.FindAllString(name, -1)
	stem := ""
	switch {
	case len(words) > 1:
		for _, word := range words {
			stem += word[:1]
		}
	case len(words) == 1:
		stem = words[0][:min(4, len(words[0]))]
	}
	stem = strings.ToUpper(notLetters.ReplaceAllString(stem, ""))
	for len(stem) < 2 {
		stem += "X"
	}
	return stem[:min(5, len(stem))]
}

// freshPrefix keeps a prefix unique by adding a digit.
func freshPrefix(base string, used map[string]bool) string {
	prefix := base
	for number := 2; used[prefix]; number++ {
		prefix = base + strconv.Itoa(number)
	}
	return prefix
}

func spaceAdd(raw []string) error {
	a, err := parseArgs(raw, []string{"folder", "base", "merge", "checks", "prefix", "brief"}, nil)
	if err != nil {
		return err
	}
	if len(a.words) != 1 {
		return usage("space add takes one name")
	}
	name := a.words[0]
	if !spaceName.MatchString(name) {
		return refuse("a space name is lowercase letters, digits, - and _")
	}
	folderFlag, ok := a.value("folder")
	if !ok {
		return usage("space add needs --folder")
	}
	folder, err := resolveDir(folderFlag)
	if err != nil {
		return err
	}
	top, _, err := run("git", "-C", folder, "rev-parse", "--show-toplevel")
	if err != nil {
		return refuse("%s is not a git repo", tilde(folder))
	}
	if top := strings.TrimSpace(top); top != folder {
		return refuse("%s is not the repo root; use %s", tilde(folder), tilde(top))
	}
	base := "main"
	if given, ok := a.value("base"); ok {
		base = given
	}
	if _, err := git("-C", folder, "rev-parse", "--verify", base+"^{commit}"); err != nil {
		return err
	}
	merge := "local"
	if given, ok := a.value("merge"); ok {
		merge = given
	}
	if merge != "local" && merge != "pr" {
		return usage("--merge is local or pr")
	}
	checks, err := checksFlag(a)
	if err != nil {
		return err
	}
	brief, err := readBrief(a.optional("brief"))
	if err != nil {
		return err
	}

	unlock, err := lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := ensureStore(); err != nil {
		return err
	}
	if exists(spacePath(name)) {
		return refuse("space %s already exists", name)
	}
	found, err := spaces()
	if err != nil {
		return err
	}
	used := map[string]bool{}
	for _, s := range found {
		if prefix := s.head.text("prefix"); prefix != "" {
			used[prefix] = true
		}
	}
	prefix := ""
	if given, ok := a.value("prefix"); ok {
		prefix = strings.ToUpper(given)
		if used[prefix] {
			taken := make([]string, 0, len(used))
			for p := range used {
				taken = append(taken, p)
			}
			sort.Strings(taken)
			return refuse("prefix %s is used by another space: %s", prefix, strings.Join(taken, ", "))
		}
	} else {
		prefix = freshPrefix(initials(name), used)
	}
	if !prefixForm.MatchString(prefix) {
		return refuse("--prefix is 2 to 5 letters")
	}
	head := header{fields: []field{
		{"folder", tilde(folder)},
		{"prefix", prefix},
		{"base", base},
		{"merge", merge},
		{"checks", checks},
		{"next", json.Number("1")},
	}}
	if id, err := repoSpace(name, folder); err != nil {
		fmt.Fprintf(os.Stderr, "shp: %v; space %s has no Herdr space yet\n", err, name)
	} else {
		head.set("herdr_space", id)
	}
	if err := writeText(spacePath(name), render(head, brief)); err != nil {
		return err
	}
	fmt.Printf("space %s: %s, %s on %s\n", name, prefix, tilde(folder), base)
	return nil
}

// resolveDir makes a path absolute and follows symlinks as far as it can.
func resolveDir(path string) (string, error) {
	abs, err := filepath.Abs(expand(path))
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real, nil
	}
	return abs, nil
}

func checksFlag(a *args) ([]any, error) {
	text, ok := a.value("checks")
	if !ok {
		return []any{}, nil
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return nil, refuse("--checks is JSON like [[\"make\", \"check\"]]")
	}
	checks, ok := parsed.([]any)
	if !ok || !isArgvList(checks) {
		return nil, refuse("--checks is JSON like [[\"make\", \"check\"]]")
	}
	return checks, nil
}
