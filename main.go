package main

import (
	"errors"
	"fmt"
	"os"
)

// main routes a command line to its command.
func main() {
	if code := dispatch(os.Args[1:]); code != 0 {
		os.Exit(code)
	}
}

var commands = map[string]func([]string) error{
	"config": configCmd,
	"space":  spaceCmd,
	"add":    addCmd,
	"list":   listCmd,
	"show":   showCmd,
	"start":  startCmd,
	"tick":   tickCmd,
	"status": reportCmd,
	"ask":    askCmd,
	"say":    sayCmd,
}

func dispatch(argv []string) int {
	if wantsHelp(argv) {
		fmt.Print(helpText(argv))
		return 0
	}
	if len(argv) == 0 {
		fmt.Fprint(os.Stderr, mainHelp)
		return 2
	}
	handler, ok := commands[argv[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "shp: unknown command %s\n", argv[0])
		return 2
	}
	err := handler(argv[1:])
	if err == nil {
		return 0
	}
	var code exitCode
	if errors.As(err, &code) {
		return int(code)
	}
	fmt.Fprintf(os.Stderr, "shp: %v\n", err)
	var bad usageError
	if errors.As(err, &bad) {
		return 2
	}
	return 1
}
