package cli

// Perintah `gar gne` — pengelola paket ekstensi Garurda Native Extension.
//
//	pack    — kemas hasil build ext/<nama> jadi zip multi-platform
//	install — pasang paket (shortcut kanal resmi, URL HTTPS, atau file)
//	list    — daftar ekstensi terpasang
//	remove  — lepas ekstensi
//
// Semua keluaran pesan dalam Bahasa Indonesia.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"garurda/internal/usecase/gnepkg"
)

// gneGlobal dipakai oleh `--force` dan `--dir` yang bisa berdiri sendiri
// setelah subperintah (flag parsar manual agar urutan bebas).
type gneOps struct {
	dir   string
	force bool
	out   string
}

// parseGneOps memisahkan flag --force/--dir/-o dari argumen posisional.
// Urutan bebas: `gar gne install --force redis` maupun
// `gar gne install redis --force`.
func parseGneOps(args []string) (gneOps, []string, error) {
	var o gneOps
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		need := func(flag string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s butuh nilai", flag)
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
			return o, nil, fmt.Errorf("flag %q tidak dikenal", a)
		default:
			pos = append(pos, a)
		}
	}
	return o, pos, nil
}

func printGneHelp(w io.Writer) {
	fmt.Fprint(w, `gar gne — pengelola ekstensi native (GNE)

Penggunaan:
  gar gne pack <dir-ekstensi> [-o keluaran.zip]   kemas build ekstensi jadi paket
  gar gne install <spesifikasi> [--force] [--dir D]  pasang paket ekstensi
  gar gne list [--dir D]                          daftar ekstensi terpasang
  gar gne remove <nama> [--dir D]                 lepas ekstensi

Spesifikasi install:
  redis                    shortcut kanal resmi (rilis terbaru)
  redis@0.6.0              shortcut versi tertentu
  https://.../redis.zip    URL HTTPS (wajib HTTPS)
  ./redis.zip              berkas paket lokal

Contoh:
  gar gne install redis
  gar gne install redis@0.6.0 --force
  gar gne pack ext/redis -o dist/redis.zip
  gar gne list
  gar gne remove redis

Direktori instalasi default: ~/.garurda/gne (dicari otomatis oleh use "nama")
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
		fmt.Fprintf(stderr, "gar gne: subperintah %q tidak dikenal\n", sub)
		printGneHelp(stderr)
		return 2
	}
}

func gnePack(ops gneOps, pos []string, stdout, stderr io.Writer) int {
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "gar gne pack: butuh tepat satu direktori ekstensi (mis. ext/redis)")
		return 2
	}
	zipPath, m, err := gnepkg.Pack(pos[0], ops.out)
	if err != nil {
		fmt.Fprintf(stderr, "gar gne pack: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "✓ paket %s %s (ABI %d) → %s\n", m.Name, m.Version, m.GNEABI, zipPath)
	fmt.Fprintf(stdout, "  platform: %s\n", daftar(m))
	return 0
}

func daftar(m *gnepkg.Manifest) string {
	s := ""
	for _, p := range sortedKeys(m) {
		if s != "" {
			s += ", "
		}
		s += p
	}
	return s
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
		fmt.Fprintln(stderr, "gar gne install: butuh tepat satu spesifikasi (nama, URL, atau berkas)")
		return 2
	}
	ctx := context.Background()
	res, err := gnepkg.Install(ctx, pos[0], gnepkg.Opsi{Dir: ops.dir, Force: ops.force})
	if err != nil {
		fmt.Fprintf(stderr, "gar gne install: %v\n", err)
		return 1
	}
	ket := "baru"
	if res.Sebelumnya {
		ket = "ditimpa"
	}
	fmt.Fprintf(stdout, "✓ %s %s terpasang (%s, %s)\n", res.Nama, res.Versi, res.Platform, ket)
	fmt.Fprintf(stdout, "  %s\n", res.Path)
	fmt.Fprintf(stdout, `  gunakan: use "%s"`, res.Nama)
	if os.Getenv("GNE_PATH") != "" {
		fmt.Fprintf(stdout, ` (perhatikan: GNE_PATH menang sebelum direktori instalasi)`)
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
		fmt.Fprintln(stdout, "belum ada ekstensi terpasang (coba: gar gne install redis)")
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAMA\tVERSI\tABI\tPLATFORM\tUKURAN")
	for _, it := range items {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\n",
			it.Nama, it.Versi, it.ABI, joinComma(it.Platform), it.Ukuran)
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
		fmt.Fprintln(stderr, "gar gne remove: butuh tepat satu nama ekstensi")
		return 2
	}
	dihapus, err := gnepkg.Remove(ops.dir, pos[0])
	if err != nil {
		fmt.Fprintf(stderr, "gar gne remove: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "✓ %s dilepas: %s\n", pos[0], dihapus)
	return 0
}
