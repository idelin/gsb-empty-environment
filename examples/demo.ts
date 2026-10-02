import { SlidingWindowQuantile } from "../src/slidingWindowQuantile.ts";

const window = new SlidingWindowQuantile(10_000); // 10 second window

console.log("=== Acceptance scenario ===");
window.add({ id: "a", timestamp: 1_000, value: 1 });
window.add({ id: "b", timestamp: 1_000, value: 1 });
window.add({ id: "c", timestamp: 1_000, value: 9 });
window.add({ id: "d", timestamp: 1_000, value: 20 });
console.log("window values:", window.values());
console.log("q=0.5 ->", window.quantile(0.5)); // 1
window.retract("a");
console.log("after retracting one '1':", window.values());
console.log("q=0.5 ->", window.quantile(0.5)); // 9

console.log("\n=== Sliding stream with inflow and expiry ===");
const stream = new SlidingWindowQuantile(10_000);
let now = 0;
let seed = 123456789;
const rand = (): number => {
  seed = (seed * 1103515245 + 12345) % 2147483648;
  return seed / 2147483648;
};
for (let i = 0; i < 24; i++) {
  now += 500 + Math.floor(rand() * 1000);
  const value = Math.floor(rand() * 100);
  stream.add({ id: `ev${i}`, timestamp: now, value });
  console.log(
    `t=${String(now).padStart(6)}ms  +${String(value).padStart(3)}  ` +
      `n=${String(stream.size).padStart(2)}  ` +
      `p50=${stream.quantile(0.5)}  p90=${stream.quantile(0.9)}  ` +
      `window=[${stream.values().join(", ")}]`,
  );
}
