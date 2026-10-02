package main

import (
	"os"
	"os/exec"
	"strings"
)

// testCmd regenerates the packages to test, then runs go test with the same
// arguments, so tests never run against stale generated code.
func testCmd(args []string) error {
	var pkgs []string
	for _, a := range args {
		if a == "." || a == ".." || strings.HasPrefix(a, "./") || strings.HasPrefix(a, "../") || strings.HasPrefix(a, "/") {
			pkgs = append(pkgs, a)
		}
	}
	if len(pkgs) == 0 {
		pkgs = []string{"."}
	}
	if err := generate(pkgs); err != nil {
		return err
	}
	cmd := exec.Command("go", append([]string{"test"}, args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return errSilent
	}
	return nil
}
