package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "Panic: %v\n", r)
			fmt.Fprintf(os.Stderr, "Stack trace:\n%s\n", debug.Stack())
			os.Exit(1)
		}
	}()

	subcommands := map[string]func([]string) error{
		"server":             runServer,
		"get-test":           getTest,
		"run-tests":          runTests,
		"generate-manifests": generateManifests,
		"show-results":       showResults,
		"export-tests":       exportTests,
	}

	var subcommand func([]string) error
	if len(os.Args) >= 2 {
		subcommand = subcommands[os.Args[1]]
	}
	if subcommand == nil {
		fmt.Printf("Usage: %s <server|get-test> ...\n", os.Args[0])
		c := make([]string, 0, len(subcommands))
		for k := range subcommands {
			c = append(c, k)
		}
		fmt.Printf("Supported sub-commands: %s\n", strings.Join(c, ", "))
		return
	}

	err := subcommand(os.Args[2:])
	if err != nil && err != flag.ErrHelp {
		panic(err)
	}
}