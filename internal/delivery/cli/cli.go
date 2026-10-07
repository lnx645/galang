// Package cli implements the `gar` command line interface.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"garurda/internal/infra/repl"
	"garurda/internal/usecase/interp"
)

// Version is the interpreter release.
const Version = "0.1.0"

// Run executes the CLI and returns a process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}
	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "-h", "--help", "help":
		printUsage(stdout)
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "gar %s — interpreter bahasa Garurda\n", Version)
		return 0
	case "run":
		return runFile(rest, stdout, stderr)
	case "repl", "shell", "sh":
		return runREPL(rest, stdin, stdout, stderr)
	}

	fmt.Fprintf(stderr, "gar: unknown command %q\n", cmd)
	printUsage(stderr)
	return 2
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `gar — interpreter bahasa Garurda

Penggunaan:
  gar run <file.ga> [argumen...]   jalankan program Garurda
  gar repl                        buka sesi interaktif (REPL)
  gar version                     tampilkan versi
  gar help                        tampilkan bantuan ini

Contoh:
  gar run examples/hello.ga
  gar run app.ga -- --port 9000
`)
}

func runFile(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "gar run: nama file .ga wajib diberikan")
		return 2
	}
	// Everything after a bare `--` is handed to the Garurda program.
	var programArgs []string
	for i, a := range args {
		if a == "--" {
			programArgs = args[i+1:]
			break
		}
	}
	file := args[0]
	if !strings.HasSuffix(file, ".ga") {
		fmt.Fprintf(stderr, "gar run: '%s' bukan berkas .ga\n", file)
		return 2
	}
	if _, err := os.Stat(file); err != nil {
		fmt.Fprintf(stderr, "gar run: %v\n", err)
		return 2
	}

	in := interp.New(stdout, stderr)
	// Positional arguments are exposed as a global `args` array.
	in.SetProgramArgs(programArgs)

	if err := in.RunFile(file); err != nil {
		report(err, stderr)
		return 1
	}
	return 0
}

// report prints an error with its source position, falling back to the plain
// message for non-runtime failures.
func report(err error, stderr io.Writer) {
	if re, ok := err.(*interp.Error); ok {
		fmt.Fprintf(stderr, "%s\n", re.Error())
		return
	}
	fmt.Fprintf(stderr, "error: %v\n", err)
}

func runREPL(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// `gar repl file.ga` preloads a script, like python's -i.
	var preload string
	fs := flag.NewFlagSet("repl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		preload = fs.Arg(0)
	}

	in := interp.New(stdout, stderr)
	if preload != "" {
		if err := in.RunFile(preload); err != nil {
			report(err, stderr)
			return 1
		}
	}
	return runSession(in, stdin, stdout, stderr)
}

func runSession(in *interp.Interp, stdin io.Reader, stdout, stderr io.Writer) int {
	r := repl.New(in, stdin, stdout)
	if err := r.Run(); err != nil {
		fmt.Fprintf(stderr, "repl: %v\n", err)
		return 1
	}
	return 0
}

// DiscoverScripts lists .ga files in a directory, used by `gar test`.
func DiscoverScripts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ga") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}
