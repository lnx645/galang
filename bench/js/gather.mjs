// gather.mjs — padanan persis dari bench/async/gather.ga (Promise.all)
async function f(n) {
  return n + 1;
}
let s = 0;
for (let b = 0; b <= 999; b++) {
  const r = await Promise.all([f(1), f(2), f(3), f(4), f(5), f(6), f(7), f(8), f(9), f(10)]);
  s += r[0] + r[9];
}
console.log(s);
