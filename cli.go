package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// refusal is a command that won't do what was asked, with the reason.
type refusal string

func (r refusal) Error() string { return string(r) }

func refuse(format string, args ...any) error {
	return refusal(fmt.Sprintf(format, args...))
}

// usageError is a command line shp cannot read; it exits 2.
type usageError string

func (u usageError) Error() string { return string(u) }

func usage(format string, args ...any) error {
	return usageError(fmt.Sprintf(format, args...))
}

// exitCode is a command's exit code when it has already printed what it had to say.
type exitCode int

func (e exitCode) Error() string { return "" }

// args is a command line after parsing: positional words, option values and flags.
type args struct {
	words  []string
	values map[string][]string
	bools  map[string]bool
}

func (a *args) value(name string) (string, bool) {
	values := a.values[name]
	if len(values) == 0 {
		return "", false
	}
	return values[len(values)-1], true
}

func (a *args) optional(name string) string {
	value, _ := a.value(name)
	return value
}

// parseArgs reads --name VALUE, --name=VALUE and --flag, plus positional words.
func parseArgs(raw, valueFlags, boolFlags []string) (*args, error) {
	knownValue := map[string]bool{}
	for _, name := range valueFlags {
		knownValue[name] = true
	}
	knownBool := map[string]bool{}
	for _, name := range boolFlags {
		knownBool[name] = true
	}
	a := &args{values: map[string][]string{}, bools: map[string]bool{}}
	for i := 0; i < len(raw); i++ {
		word := raw[i]
		switch {
		case word == "--":
			a.words = append(a.words, raw[i+1:]...)
			return a, nil
		case strings.HasPrefix(word, "--"):
			name, value, hasValue := strings.Cut(word[2:], "=")
			if knownBool[name] {
				if hasValue {
					return nil, usage("--%s takes no value", name)
				}
				a.bools[name] = true
				continue
			}
			if !knownValue[name] {
				return nil, usage("unknown option --%s", name)
			}
			if !hasValue {
				i++
				if i == len(raw) {
					return nil, usage("--%s needs a value", name)
				}
				value = raw[i]
			}
			a.values[name] = append(a.values[name], value)
		case strings.HasPrefix(word, "-") && word != "-":
			return nil, usage("unknown option %s", word)
		default:
			a.words = append(a.words, word)
		}
	}
	return a, nil
}

// printJSON prints a value as indented JSON.
func printJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// run runs a program and keeps its output.
func run(name string, args ...string) (stdout, stderr string, err error) {
	var out, errOut bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func git(args ...string) (string, error) {
	out, errOut, err := run("git", args...)
	if err != nil {
		reason := lastLine(errOut)
		if reason == "" {
			reason = "failed"
		}
		return "", refuse("git %s: %s", args[0], reason)
	}
	return out, nil
}

func hc(args ...string) (string, error) {
	out, errOut, err := run("hc", args...)
	if err != nil {
		return "", refuse("hc %s: %s", args[0], reason(errOut, out))
	}
	return out, nil
}

func hcJSON(args ...string) (map[string]any, error) {
	out, err := hc(args...)
	if err != nil {
		return nil, err
	}
	var reply map[string]any
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		return nil, refuse("hc %s printed no JSON", args[0])
	}
	return reply, nil
}

// herdrApp asks herdr-app to do something and returns its reply.
func herdrApp(command string, payload any) (map[string]any, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	out, errOut, err := run("herdr-app", "do", command, string(data))
	reply := map[string]any{}
	_ = json.Unmarshal([]byte(out), &reply)
	reported, _ := reply["error"].(string)
	if err == nil && reported == "" {
		return reply, nil
	}
	message := reported
	if message == "" {
		message = reason(errOut, out)
	}
	return nil, refuse("herdr-app %s: %s", command, message)
}

// reason is the last useful line of a failed command's output.
func reason(stderr, stdout string) string {
	if line := lastLine(stderr); line != "" {
		return line
	}
	if line := lastLine(stdout); line != "" {
		return line
	}
	return "failed"
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func configCmd(raw []string) error {
	if len(raw) > 0 {
		return usage("usage: shp config")
	}
	path := filepath.Join(store(), "config.md")
	if !isFile(path) {
		fmt.Print(render(defaultConfig, ""))
		return nil
	}
	text, err := readText(path)
	if err != nil {
		return err
	}
	fmt.Print(text)
	return nil
}

func spaceCmd(raw []string) error {
	if len(raw) == 0 {
		return usage("usage: shp space {add} ...")
	}
	if raw[0] == "add" {
		return spaceAdd(raw[1:])
	}
	return usage("unknown space command %s", raw[0])
}

// wantsHelp reports whether -h or --help was asked for; after -- they are values.
func wantsHelp(argv []string) bool {
	for _, arg := range argv {
		if arg == "--" {
			return false
		}
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func helpText(argv []string) string {
	command := ""
	if len(argv) > 0 {
		command = argv[0]
	}
	if command == "space" && len(argv) > 1 && argv[1] == "add" {
		return spaceAddHelp
	}
	switch command {
	case "config":
		return configHelp
	case "space":
		return spaceHelp
	case "add":
		return addHelp
	case "list":
		return listHelp
	case "show":
		return showHelp
	case "start":
		return startHelp
	case "tick":
		return tickHelp
	case "status":
		return statusHelp
	case "ask":
		return askHelp
	case "say":
		return sayHelp
	}
	return mainHelp
}

const mainHelp = `usage: shp [-h] {config,space,add,list,show,start,tick,status,ask,say} ...

Spaces and tasks for coding agents on Herdr.

positional arguments:
  {config,space,add,list,show,start,tick,status,ask,say}
    config              print the config file
    space               spaces are git repos
    add                 add a task
    list                one line per task
    show                print a task
    start               make the worktree and start the worker
    tick                start queued tasks that may run
    status              a worker reports its progress
    ask                 a worker asks the person
    say                 send a person's answer to the worker

options:
  -h, --help            show this help message and exit
`

const configHelp = `usage: shp config [-h]

options:
  -h, --help  show this help message and exit
`

const spaceHelp = `usage: shp space [-h] {add} ...

positional arguments:
  {add}
    add       add a space

options:
  -h, --help  show this help message and exit
`

const spaceAddHelp = `usage: shp space add [-h] --folder FOLDER [--base BASE] [--merge {local,pr}]
                     [--checks CHECKS] [--prefix PREFIX] [--brief BRIEF]
                     name

positional arguments:
  name

options:
  -h, --help          show this help message and exit
  --folder FOLDER
  --base BASE
  --merge {local,pr}
  --checks CHECKS     JSON argv lists, e.g. [["make", "check"]]
  --prefix PREFIX     2-5 letters; derived from the name when left out
  --brief BRIEF       its brief file, - for stdin
`

const addHelp = `usage: shp add [-h] [--space SPACE] [--brief BRIEF] [--status {todo,queued}]
               [--parent PARENT] [--after AFTER] [--agent AGENT]
               title

positional arguments:
  title

options:
  -h, --help            show this help message and exit
  --space SPACE
  --brief BRIEF         its brief file, - for stdin
  --status {todo,queued}
  --parent PARENT
  --after AFTER
  --agent AGENT         argv JSON, e.g. ["claude"]; empty runs the config
                        worker
`

const listHelp = `usage: shp list [-h] [--json] [--space SPACE]

options:
  -h, --help     show this help message and exit
  --json         the header's keys
  --space SPACE
`

const showHelp = `usage: shp show [-h] [--json] id

positional arguments:
  id

options:
  -h, --help  show this help message and exit
  --json      the header's keys
`

const startHelp = `usage: shp start [-h] id

positional arguments:
  id

options:
  -h, --help  show this help message and exit
`

const tickHelp = `usage: shp tick [-h]

options:
  -h, --help  show this help message and exit
`

const statusHelp = `usage: shp status [-h] [--progress PROGRESS] [--step STEP] [--left LEFT] id

positional arguments:
  id

options:
  -h, --help           show this help message and exit
  --progress PROGRESS
  --step STEP
  --left LEFT
`

const askHelp = `usage: shp ask [-h] id text

positional arguments:
  id
  text

options:
  -h, --help  show this help message and exit
`

const sayHelp = `usage: shp say [-h] [--ask ASK] id text

positional arguments:
  id
  text

options:
  -h, --help  show this help message and exit
  --ask ASK   the ask to close; needed when several are open
`
