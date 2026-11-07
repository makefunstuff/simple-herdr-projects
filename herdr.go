package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// herdrSpace is a space as herdr-app ls lists it; Machine is empty on this computer.
type herdrSpace struct {
	ID      string
	Folder  string
	Machine string
	Tabs    []struct{ Name string }
}

// taskTabs are the tabs a task's space gets after Chat. The mods follow the
// space's folder; a tab whose mod is not installed is a shell there instead.
var taskTabs = []struct{ name, mod string }{
	{"Terminal", ""},
	{"Files", "px-files"},
	{"Changes", "px-changes"},
}

// herdrSpaces lists the spaces in Herdr's window.
func herdrSpaces() ([]herdrSpace, error) {
	out, errOut, err := run("herdr-app", "ls")
	if err != nil {
		return nil, refuse("herdr-app ls: %s", reason(errOut, out))
	}
	var listed struct{ Spaces []herdrSpace }
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return nil, refuse("herdr-app ls printed no JSON")
	}
	return listed.Spaces, nil
}

// openSpace is the space with this id, or nil when it is not open.
func openSpace(id string) (*herdrSpace, error) {
	listed, err := herdrSpaces()
	if err != nil {
		return nil, err
	}
	for i := range listed {
		if listed[i].ID == id {
			return &listed[i], nil
		}
	}
	return nil, nil
}

// repoSpace is the id of the Herdr space at a repo's checkout, made when
// there is none: Herdr nests each worktree's space under the first space at
// its checkout, so this is what puts task spaces under the repo.
func repoSpace(name, repo string) (string, error) {
	listed, err := herdrSpaces()
	if err != nil {
		return "", err
	}
	for _, s := range listed {
		if s.Machine != "" || s.Folder == "" {
			continue
		}
		if folder, err := resolveDir(s.Folder); err == nil && folder == repo {
			return s.ID, nil
		}
	}
	reply, err := herdrApp("space.new", map[string]any{"name": name, "folder": repo})
	if err != nil {
		return "", err
	}
	id, _ := reply["space"].(string)
	if id == "" {
		return "", refuse("herdr-app made no space for %s", tilde(repo))
	}
	return id, nil
}

// openTab adds a tab to a task's space: the mod when it is installed, else a
// shell in the worktree.
func openTab(workspace, worktree, name, mod string) error {
	payload := map[string]any{"name": name, "space": workspace}
	if mod != "" && isFile(filepath.Join(appHome(), "mods", mod, "main.js")) {
		payload["mod"] = mod
		payload["props"] = map[string]any{"folder": worktree} // px-pr reads it; the others follow the space
	} else {
		payload["folder"] = worktree
	}
	_, err := herdrApp("tab.new", payload)
	return err
}

// appHome is herdr-app's own folder, where its mods are installed.
func appHome() string {
	if home := os.Getenv("HERDR_APP_HOME"); home != "" {
		return home
	}
	return filepath.Join(homeDir(), ".config", "herdr-app")
}

// openTaskTabs gives a new task space its tabs after Chat; one that will not
// open is named and left out.
func openTaskTabs(t task, workspace string) {
	for _, tab := range taskTabs {
		if err := openTab(workspace, expand(t.head.text("worktree")), tab.name, tab.mod); err != nil {
			fmt.Fprintf(os.Stderr, "shp: %v; %s has no %s tab\n", err, t.id, tab.name)
		}
	}
}

// openPRTab gives a task's space its PR tab, unless it has one.
func openPRTab(t task) error {
	workspace := t.head.text("workspace")
	if workspace == "" {
		return nil
	}
	space, err := openSpace(workspace)
	if err != nil || space == nil {
		return err
	}
	for _, tab := range space.Tabs {
		if tab.Name == "PR" {
			return nil
		}
	}
	return openTab(workspace, expand(t.head.text("worktree")), "PR", "px-pr")
}

// closeSpace closes a task's Herdr space, if it is still open, ending
// whatever runs in it. Only that one space goes: the repo's and those made by
// hand stay. Callers kill the task's terminals first, so no live task
// terminal holds the space while it goes.
func closeSpace(id, workspace string) {
	if workspace == "" {
		return
	}
	space, err := openSpace(workspace)
	if err == nil && space != nil {
		_, err = herdrApp("space.close", map[string]any{"id": workspace, "confirmed": true})
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "shp: %v; %s's space %s may still be open\n", err, id, workspace)
	}
}
