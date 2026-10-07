<?php
// loop.php — padanan persis dari bench/loop.ga
// src: $total = 0; for $i in 0..200000 { $total = $total + $i * 2 - 1 }

$total = 0;
for ($i = 0; $i <= 200000; $i++) {
    $total = $total + $i * 2 - 1;
}
echo $total, "\n";
