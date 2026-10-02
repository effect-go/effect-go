// Command ego compiles and formats effect-go (.ego) files.
//
//	ego generate [packages]   write the Go for each .ego file (default ./...)
//	ego fmt [-l] [-w] [paths] format .ego files (default: the current directory, recursively)
//	ego test [args]           ego generate, then go test with the same arguments
//	ego lsp                   run the language server proxy in front of gopls
//	ego version
package main

import (
	"flag"
	"fmt"
	"os"
)

const version = "v0.1.0"

func usage() {
	fmt.Fprint(os.Stderr, `ego compiles effect-go (.ego) files to Go.

Usage:
	ego generate [-check] [dirs]         generate x_ego.go for each x.ego, and layers_ego.go (default ./...)
	ego fmt [-l] [-w] [paths]            format .ego files (default ./...)
	ego test [go test flags] [packages]  ego generate, then go test with the same arguments
	ego lsp                              language server: gopls with .ego support
	ego version
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "generate", "gen":
		err = generate(args)
	case "fmt":
		err = fmtCmd(args)
	case "test":
		err = testCmd(args)
	case "lsp":
		err = lspCmd(args)
	case "version":
		fmt.Println("ego", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "ego: unknown command %q\n", cmd)
		usage()
	}
	if err != nil {
		if err != errSilent {
			fmt.Fprintln(os.Stderr, "ego:", err)
		}
		os.Exit(1)
	}
}

var errSilent = fmt.Errorf("failed")

var _ = flag.Parse
