// Package repl implements the interactive Garurda shell.
package repl

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"garurda/internal/usecase/interp"
)

// REPL runs an interactive session.
type REPL struct {
	in *interp.Interp
	r  *bufio.Reader
	w  io.Writer
}

// New creates a REPL bound to an interpreter.
func New(in *interp.Interp, r io.Reader, w io.Writer) *REPL {
	return &REPL{in: in, r: bufio.NewReader(r), w: w}
}

// Run loops until EOF or an explicit `exit`.
func (r *REPL) Run() error {
	fmt.Fprintln(r.w, "Garurda REPL — ketik .exit untuk keluar, .help untuk bantuan")
	for {
		fmt.Fprint(r.w, "gar> ")
		line, err := r.r.ReadString('\n')
		if err != nil && line == "" {
			if err == io.EOF {
				fmt.Fprintln(r.w)
				return nil
			}
			return err
		}
		src := strings.TrimSpace(line)
		if src == "" {
			continue
		}
		switch src {
		case ".exit", ".quit", "exit", "quit":
			return nil
		case ".help", "help":
			r.help()
			continue
		}
		// An unfinished block keeps reading, so multi-line functions work.
		for strings.Count(src, "{") > strings.Count(src, "}") && err == nil {
			fmt.Fprint(r.w, "  ... ")
			next, nerr := r.r.ReadString('\n')
			src += "\n" + next
			err = nerr
		}
		val, evalErr := r.in.Eval(src, "<repl>")
		if evalErr != nil {
			fmt.Fprintf(r.w, "error: %v\n", evalErr)
		} else if val != nil && !r.wroteOutput(src) {
			// Echo a bare expression's value, as a REPL should. `print`
			// already produced its own output.
			fmt.Fprintf(r.w, "%s\n", val.String())
		}
		if err == io.EOF {
			fmt.Fprintln(r.w)
			return nil
		}
	}
}

// wroteOutput reports whether the snippet already printed something itself,
// in which case the REPL must not echo the value again.
func (r *REPL) wroteOutput(src string) bool {
	return strings.HasPrefix(strings.TrimSpace(src), "print")
}

func (r *REPL) help() {
	fmt.Fprint(r.w, `Perintah:
  .help            tampilkan bantuan ini
  .exit            keluar dari REPL

Contoh:
  $nama = "Garurda"
  print "Halo ${nama}"
  fn greet(x) => "Halo, ${x}"
  print greet("dunia")
`)
}

// Ensure bufio and os stay referenced for future line editing support.
var _ = os.Stdin
