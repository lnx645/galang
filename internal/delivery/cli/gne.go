package cli

// Command `gar gne` — manager for GaLang Native Extension packages.
//
//	pack    — package the build in ext/<name> into a multi-platform zip
//	install — install a package (official-channel shortcut, HTTPS URL, or file)
//	list    — list installed extensions
//	remove  — remove an extension
//
// All output messages are in English.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"galang/internal/usecase/gnepkg"
)

// gneGlobal backs `--force` and `--dir`, which can stand alone
// after the subcommand (manual flag parsing so the order is free).
type gneOps struct {
	dir   string
	force bool
	out   string
}

// parseGneOps separates the --force/--dir/-o flags from positional arguments.
// Order is free: both `gar gne install --force redis` and
// `gar gne install redis --force` are accepted.
func parseGneOps(args []string) (gneOps, []string, error) {
	var o gneOps
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		need := func(flag string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s requires a value", flag)
			}
			i++
			return args[i], nil
		}
		switch {
		case a == "--force":
			o.force = true
		case a == "--dir":
			v, err := need("--dir")
			if err != nil {
				return o, nil, err
			}
			o.dir = v
		case strings.HasPrefix(a, "--dir="):
			o.dir = a[len("--dir="):]
		case a == "-o":
			v, err := need("-o")
			if err != nil {
				return o, nil, err
			}
			o.out = v
		case strings.HasPrefix(a, "-o="):
			o.out = a[len("-o="):]
		case strings.HasPrefix(a, "--"):
			return o, nil, fmt.Errorf("unknown flag %q", a)
		default:
			pos = append(pos, a)
		}
	}
	return o, pos, nil
}

func printGneHelp(w io.Writer) {
	fmt.Fprint(w, `gar gne — native extension manager (GNE)

Usage:
  gar gne pack <extension-dir> [-o output.zip]    package a build into a zip
  gar gne install <spec> [--force] [--dir D]         install an extension package
  gar gne list [--dir D]                          list installed extensions
  gar gne remove <name> [--dir D]                 remove an extension

Install specification:
  redis | smtp             official-channel shortcut (latest release)
  redis@0.6.0              pinned version
  https://.../redis.zip    HTTPS URL (HTTPS required)
  ./redis.zip              local package file

Examples:
  gar gne install redis
  gar gne install redis@0.6.0 --force
  gar gne pack ext/redis -o dist/redis.zip
  gar gne list
  gar gne remove redis

Default install directory: ~/.galang/gne (found automatically by use "name";
the old ~/.garurda/gne location from before v0.7.0 is still read)
`)
}

func runGNE(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printGneHelp(stdout)
		return 0
	}
	sub, rest := args[0], args[1:]

	ops, pos, err := parseGneOps(rest)
	if err != nil {
		fmt.Fprintf(stderr, "gar gne %s: %v\n", sub, err)
		return 2
	}

	switch sub {
	case "help", "-h", "--help":
		printGneHelp(stdout)
		return 0
	case "pack":
		return gnePack(ops, pos, stdout, stderr)
	case "install", "add":
		return gneInstall(ops, pos, stdout, stderr)
	case "list", "ls":
		return gneList(ops, stdout, stderr)
	case "remove", "rm", "uninstall":
		return gneRemove(ops, pos, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "gar gne: unknown subcommand %q\n", sub)
		printGneHelp(stderr)
		return 2
	}
}

func gnePack(ops gneOps, pos []string, stdout, stderr io.Writer) int {
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "gar gne pack: requires exactly one extension directory (e.g. ext/redis)")
		return 2
	}
	zipPath, m, err := gnepkg.Pack(pos[0], ops.out)
	if err != nil {
		fmt.Fprintf(stderr, "gar gne pack: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "✓ package %s %s (ABI %d) → %s\n", m.Name, m.Version, m.GNEABI, zipPath)
	fmt.Fprintf(stdout, "  platform: %s\n", joinComma(sortedKeys(m)))
	return 0
}

func sortedKeys(m *gnepkg.Manifest) []string {
	keys := make([]string, 0, len(m.Entry))
	for k := range m.Entry {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func gneInstall(ops gneOps, pos []string, stdout, stderr io.Writer) int {
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "gar gne install: requires exactly one specification (name, URL, or file)")
		return 2
	}
	ctx := context.Background()
	res, err := gnepkg.Install(ctx, pos[0], gnepkg.Options{Dir: ops.dir, Force: ops.force})
	if err != nil {
		fmt.Fprintf(stderr, "gar gne install: %v\n", err)
		return 1
	}
	status := "new"
	if res.Previous {
		status = "overwritten"
	}
	fmt.Fprintf(stdout, "✓ %s %s installed (%s, %s)\n", res.Name, res.Version, res.Platform, status)
	fmt.Fprintf(stdout, "  %s\n", res.Path)
	fmt.Fprintf(stdout, `  use it with: use "%s"`, res.Name)
	if os.Getenv("GNE_PATH") != "" {
		fmt.Fprintf(stdout, ` (note: GNE_PATH takes precedence over the install directory)`)
	}
	fmt.Fprintln(stdout)
	return 0
}

func gneList(ops gneOps, stdout, stderr io.Writer) int {
	items, err := gnepkg.List(ops.dir)
	if err != nil {
		fmt.Fprintf(stderr, "gar gne list: %v\n", err)
		return 1
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, "no extensions installed yet (try: gar gne install redis)")
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tVERSION\tABI\tPLATFORM\tSIZE")
	for _, it := range items {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\n",
			it.Name, it.Version, it.ABI, joinComma(it.Platform), it.Size)
	}
	tw.Flush()
	return 0
}

func joinComma(ss []string) string {
	s := ""
	for i, v := range ss {
		if i > 0 {
			s += ","
		}
		s += v
	}
	return s
}

func gneRemove(ops gneOps, pos []string, stdout, stderr io.Writer) int {
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "gar gne remove: requires exactly one extension name")
		return 2
	}
	removed, err := gnepkg.Remove(ops.dir, pos[0])
	if err != nil {
		fmt.Fprintf(stderr, "gar gne remove: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "✓ %s removed: %s\n", pos[0], removed)
	return 0
}
