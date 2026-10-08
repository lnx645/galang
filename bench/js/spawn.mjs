// spawn.mjs — padanan persis dari bench/async/spawn.ga
async function f(n) {
  return n + 1;
}
for (let i = 0; i <= 99999; i++) {
  f(i); // fire-and-forget: hasil tidak di-await
}
// Pastikan semua promise selesai sebelum keluar.
await new Promise((r) => setImmediate(r));
console.log("selesai");
