// ref.go — referensi native Go untuk program yang sama.
// Dipakai sebagai patokan: Garurda tidak mungkin menyamai ini pada beban CPU,
// tapi angkanya penting untuk konteks.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func fib(n int) int {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "loop" {
		total := 0
		for i := 0; i <= 200000; i++ {
			total = total + i*2 - 1
		}
		fmt.Println(total)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "call" {
		n, _ := strconv.Atoi(os.Args[2])
		fmt.Println(sumTo(n))
		return
	}
	fmt.Println(fib(25))
}

func sumTo(n int) int {
	acc := 0
	for i := 0; i <= n; i++ {
		acc = acc + i
	}
	return acc
}
