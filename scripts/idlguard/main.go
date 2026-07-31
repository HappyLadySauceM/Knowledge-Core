// Command idlguard discovers service-bearing Thrift files and rejects
// backwards-incompatible IDL changes.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}

	switch args[0] {
	case "services":
		if len(args) != 2 {
			return usage()
		}
		files, err := serviceFiles(args[1])
		if err != nil {
			return err
		}
		for _, file := range files {
			fmt.Println(filepath.ToSlash(filepath.Join(args[1], file)))
		}
		return nil
	case "compat":
		if len(args) != 3 {
			return usage()
		}
		return compareTrees(args[1], args[2])
	case "compat-git":
		if len(args) != 3 {
			return usage()
		}
		repositoryRoot, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve repository root: %w", err)
		}
		return compareGit(repositoryRoot, args[1], args[2])
	default:
		return usage()
	}
}

func usage() error {
	return fmt.Errorf("usage: idlguard services <root> | compat <base-root> <current-root> | compat-git <revision> <current-root>")
}
