package main

import (
	"context"
	"os"

	"github.com/codeledger/codeledger/cmd"
)

func main() {
	// cmd.Execute builds a fresh command tree, renders any error exactly once
	// (JSON envelope for --json commands, one text line on stderr otherwise),
	// and returns the stable process exit code. main only exits with it.
	os.Exit(cmd.Execute(context.Background(), cmd.NewDependencies(), os.Args[1:]))
}
