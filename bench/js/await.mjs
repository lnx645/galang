// await.mjs — padanan persis dari bench/async/await.ga
async function f(n) {
  return n + 1;
}
let total = 0;
for (let i = 0; i <= 100000; i++) {
  total = await f(total);
}
console.log(total);
