<?php
// fib.php — padanan persis dari bench/fib.ga
// src: fn fib($n) { if $n < 2 { return $n } return fib($n-1) + fib($n-2) }

function fib(int $n): int {
    if ($n < 2) { return $n; }
    return fib($n - 1) + fib($n - 2);
}

$hasil = fib(25);
echo $hasil, "\n";
