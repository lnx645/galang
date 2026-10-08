// call.mjs — KONTROL: padanan persis dari bench/async/call.ga
// (loop dan beban sama, tanpa async/await).
function f(n) {
  return n + 1;
}
let total = 0;
for (let i = 0; i <= 100000; i++) {
  total = f(total);
}
console.log(total);
