package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/effect-go/effect-go/internal/syntax/format"
)

func fmtCmd(args []string) error {
	fl := flag.NewFlagSet("fmt", flag.ExitOnError)
	list := fl.Bool("l", false, "list files whose formatting differs")
	write := fl.Bool("w", false, "write the result to the file")
	fl.Parse(args)
	paths := fl.Args()
	if len(paths) == 0 {
		if !*list && !*write {
			// Like gofmt: format stdin to stdout.
			src, err := readAll(os.Stdin)
			if err != nil {
				return err
			}
			out, err := format.Source(src)
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(out)
			return err
		}
		paths = []string{"."}
	}
	failed := false
	for _, root := range paths {
		err := filepath.WalkDir(strings.TrimSuffix(root, "/..."), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "testdata") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".ego") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out, err := format.Source(src)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				failed = true
				return nil
			}
			if bytes.Equal(src, out) {
				return nil
			}
			if *list {
				fmt.Println(path)
			}
			if *write {
				return os.WriteFile(path, out, 0o666)
			}
			if !*list {
				os.Stdout.Write(out)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if failed {
		return errSilent
	}
	return nil
}

func readAll(f *os.File) ([]byte, error) {
	var b bytes.Buffer
	_, err := b.ReadFrom(f)
	return b.Bytes(), err
}
