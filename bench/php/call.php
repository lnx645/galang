<?php
// call.php — padanan persis dari bench/call.ga
// src: fn kerja($n) { $acc = 0; for $i in 0..$n { $acc = $acc + $i } return $acc }

function kerja(int $n): int {
    $acc = 0;
    // `for $i in 0..$n` in GaLang is inclusive on both ends, so the
    // equivalent PHP loop uses `<=`.
    for ($i = 0; $i <= $n; $i++) {
        $acc = $acc + $i;
    }
    return $acc;
}

$hasil = kerja(100000);
echo $hasil, "\n";
