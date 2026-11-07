package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// store is the directory that holds everything shp knows.
func store() string { return filepath.Join(homeDir(), ".herdr", "simple") }

func homeDir() string { return os.Getenv("HOME") }

// tilde writes a path with ~ for the home directory, so files carry no machine names.
func tilde(path string) string {
	home := homeDir()
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}

// expand replaces a leading ~ with the home directory.
func expand(path string) string {
	if path == "~" {
		return homeDir()
	}
	if strings.HasPrefix(path, "~/") {
		return homeDir() + path[1:]
	}
	return path
}

// wrap names the file an error is about.
func wrap(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", tilde(path), err)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// readText reads a store file, naming it with ~ in errors.
func readText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		return "", wrap(path, err)
	}
	return string(data), nil
}

// writeText replaces a file in one rename: a reader sees the old file or the new one.
func writeText(path, text string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return wrap(path, err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp")
	if err != nil {
		return wrap(path, err)
	}
	name := temp.Name()
	defer os.Remove(name) // after a successful rename there is nothing left to remove
	_, err = temp.WriteString(text)
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return wrap(path, err)
	}
	return wrap(path, os.Rename(name, path))
}

// lock takes the store's exclusive lock for a read-modify-write; closing releases it.
func lock() (func(), error) {
	dir := store()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, wrap(dir, err)
	}
	file, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, wrap(dir, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, wrap(dir, err)
	}
	return func() { file.Close() }, nil
}

// header is a Markdown file's head: key/value fields in file order.
type header struct{ fields []field }

type field struct {
	key   string
	value any
}

func (h header) val(key string) any {
	for _, f := range h.fields {
		if f.key == key {
			return f.value
		}
	}
	return nil
}

func (h header) has(key string) bool {
	for _, f := range h.fields {
		if f.key == key {
			return true
		}
	}
	return false
}

func (h *header) set(key string, value any) {
	for i := range h.fields {
		if h.fields[i].key == key {
			h.fields[i].value = value
			return
		}
	}
	h.fields = append(h.fields, field{key, value})
}

// text is a string field; a missing or malformed one reads as "".
func (h header) text(key string) string {
	text, _ := h.val(key).(string)
	return text
}

func (h header) number(key string) (int, bool) { return asInt(h.val(key)) }

// list is a list field, or nil when it is missing or not a list.
func (h header) list(key string) []any {
	items, _ := h.val(key).([]any)
	return items
}

// words is a list of strings, or nil.
func (h header) words(key string) []string {
	words, _ := asStrings(h.val(key))
	return words
}

// MarshalJSON writes a header as a JSON object with its keys in file order.
func (h header) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, f := range h.fields {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(f.key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(f.value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.key, err)
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// parseFile reads a file's header and the Markdown below it.
func parseFile(path string) (header, string, error) {
	text, err := readText(path)
	if err != nil {
		return header{}, "", err
	}
	lines := strings.Split(text, "\n")
	if trimCR(lines[0]) != "---" {
		return header{}, "", refuse("%s: no header (a first line of ---)", tilde(path))
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if trimCR(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return header{}, "", refuse("%s: header has no closing ---", tilde(path))
	}
	var h header
	for i := 1; i < end; i++ {
		key, raw, ok := strings.Cut(trimCR(lines[i]), ":")
		if !ok {
			return header{}, "", refuse("%s:%d: expected key: value", tilde(path), i+1)
		}
		h.set(strings.TrimSpace(key), scalar(strings.TrimSpace(raw)))
	}
	return h, strings.Join(lines[end+1:], "\n"), nil
}

func trimCR(line string) string { return strings.TrimSuffix(line, "\r") }

// scalar reads a header value: JSON when it parses, otherwise the plain text.
func scalar(text string) any {
	if !json.Valid([]byte(text)) {
		return text
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return text
	}
	return value
}

// render writes a file with its header and body.
func render(h header, body string) string {
	var text strings.Builder
	text.WriteString("---\n")
	for _, f := range h.fields {
		fmt.Fprintf(&text, "%s: %s\n", f.key, quote(f.value))
	}
	text.WriteString("---\n")
	text.WriteString(body)
	return text.String()
}

// quote writes a value as a header value: bare when it reads back the same, JSON otherwise.
func quote(value any) string {
	if text, ok := value.(string); ok && text != "" && text == strings.TrimSpace(text) &&
		!strings.Contains(text, "\n") && !strings.HasPrefix(text, "#") &&
		!json.Valid([]byte(text)) {
		return text
	}
	var out strings.Builder
	writeJSON(&out, value)
	return out.String()
}

// writeJSON writes a value with a space after each comma and colon, the way
// the store's files have always been written.
func writeJSON(out *strings.Builder, value any) {
	switch v := value.(type) {
	case []any:
		out.WriteString("[")
		for i, item := range v {
			if i > 0 {
				out.WriteString(", ")
			}
			writeJSON(out, item)
		}
		out.WriteString("]")
	case map[string]any:
		out.WriteString("{")
		for i, key := range slices.Sorted(maps.Keys(v)) {
			if i > 0 {
				out.WriteString(", ")
			}
			writeJSON(out, key)
			out.WriteString(": ")
			writeJSON(out, v[key])
		}
		out.WriteString("}")
	default:
		var data bytes.Buffer
		encoder := json.NewEncoder(&data)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(v) // the rest are strings, numbers, booleans and null
		out.WriteString(strings.TrimSuffix(data.String(), "\n"))
	}
}

// defaults are config.md's defaults, in the order the file lists them.
var defaultConfig = header{fields: []field{
	{"coordinator", []any{"pi"}},
	{"worker", []any{"pi"}},
	{"reviewer", []any{"pi"}},
	{"must_fix", "major"},
	{"rounds", json.Number("3")},
	{"max_running", json.Number("4")},
}}

// config loads config.md over the defaults, checking the roles and the numbers.
func config() (header, error) {
	cfg := header{fields: append([]field(nil), defaultConfig.fields...)}
	path := filepath.Join(store(), "config.md")
	if !exists(path) {
		return cfg, nil
	}
	file, _, err := parseFile(path)
	if err != nil {
		return cfg, err
	}
	for _, f := range file.fields {
		cfg.set(f.key, f.value)
	}
	for _, role := range []string{"coordinator", "worker", "reviewer"} {
		if words, ok := asStrings(cfg.val(role)); !ok || len(words) == 0 {
			return cfg, refuse("%s: %s is an argv list", tilde(path), role)
		}
	}
	for _, key := range []string{"max_running", "rounds"} {
		if _, ok := cfg.number(key); !ok {
			return cfg, refuse("%s: %s is a number", tilde(path), key)
		}
	}
	return cfg, nil
}

// ensureStore writes a config file before the store's first use.
func ensureStore() error {
	path := filepath.Join(store(), "config.md")
	if exists(path) {
		return nil
	}
	return writeText(path, render(defaultConfig, ""))
}

// readBrief reads a brief file, or stdin for "-"; an empty brief is no brief.
func readBrief(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	var text string
	if name == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", refuse("-: %v", err)
		}
		text = string(data)
	} else {
		read, err := readText(expand(name))
		if err != nil {
			return "", err
		}
		text = read
	}
	if strings.TrimSpace(text) == "" {
		return "", nil
	}
	return strings.Trim(text, "\n") + "\n", nil
}

// asStrings reads a JSON list of strings.
func asStrings(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	words := make([]string, len(items))
	for i, item := range items {
		word, ok := item.(string)
		if !ok {
			return nil, false
		}
		words[i] = word
	}
	return words, true
}

// asInt reads an integer header value.
func asInt(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(number.String())
	return n, err == nil
}

// rule is what one task or space field may hold.
type rule struct {
	key  string
	ok   func(any) bool
	want string
}

var taskRules = []rule{
	{"title", isString, "a string"},
	{"branch", isStringOrNil, "a branch"},
	{"worktree", isStringOrNil, "a path"},
	{"parent", isStringOrNil, "a task id"},
	{"after", isStringsOrNil, "a list of task ids"},
	{"agent", isStringsOrNil, "an argv list"},
	{"asks", isAsks, "a list of id, text and time"},
	{"status", isString, "a word"},
	{"progress", isProgress, "0 to 100"},
	{"step", isStringOrNil, "a string"},
	{"left", isStringOrNil, "a string"},
	{"workspace", isStringOrNil, "an id"},
}

var spaceRules = []rule{
	{"folder", isStringOrNil, "a path"},
	{"prefix", isStringOrNil, "letters"},
	{"base", isStringOrNil, "a branch"},
	{"checks", isArgvList, "argv lists"},
	{"next", isIntOrNil, "a number"},
}

// checkFields refuses a file whose known fields hold the wrong kind of value.
func checkFields(path string, h header, rules []rule) error {
	for _, r := range rules {
		if h.has(r.key) && !r.ok(h.val(r.key)) {
			return refuse("%s: %s is %s", tilde(path), r.key, r.want)
		}
	}
	return nil
}

func isString(value any) bool {
	_, ok := value.(string)
	return ok
}

func isStringOrNil(value any) bool { return value == nil || isString(value) }

func isStrings(value any) bool {
	_, ok := asStrings(value)
	return ok
}

func isStringsOrNil(value any) bool { return value == nil || isStrings(value) }

func isInt(value any) bool {
	_, ok := asInt(value)
	return ok
}

func isIntOrNil(value any) bool { return value == nil || isInt(value) }

func isProgress(value any) bool {
	n, ok := asInt(value)
	return value == nil || ok && n >= 0 && n <= 100
}

func isAsks(value any) bool {
	if value == nil {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		ask, ok := item.(map[string]any)
		if !ok {
			return false
		}
		for _, key := range []string{"id", "text", "time"} {
			if !isString(ask[key]) {
				return false
			}
		}
	}
	return true
}

func isArgvList(value any) bool {
	if value == nil {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if !isStrings(item) {
			return false
		}
	}
	return true
}
