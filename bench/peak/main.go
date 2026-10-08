// Command peak menjalankan perintah apa pun dan mencetak RSS puncaknya
// (VmHWM, KiB) lewat wait4/getrusage — akurat, bukan sampling.
//
//	penggunaan: peak COMMAND [ARGS...]
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "penggunaan: peak COMMAND [ARGS...]")
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok && cmd.ProcessState != nil {
			os.Exit(cmd.ProcessState.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Maxrss sudah dikumpulkan proses anak saat ia di-reap oleh Run().
	if cmd.ProcessState != nil {
		if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			fmt.Println(ru.Maxrss) // KiB di Linux
		}
	}
}
