import { SlidingWindowQuantile } from "../src/slidingWindowQuantile.ts";

const win = new SlidingWindowQuantile(10_000); // 10-second sliding window

console.log("== acceptance scenario: [1, 1, 9, 20] ==");
win.add({ id: "a", timestamp: 1_000, value: 1 });
win.add({ id: "b", timestamp: 2_000, value: 1 });
win.add({ id: "c", timestamp: 3_000, value: 9 });
win.add({ id: "d", timestamp: 4_000, value: 20 });
console.log("q=0.5 ->", win.quantile(0.5)); // 1

win.retract("a"); // retract one of the 1s
console.log("after retracting one 1, q=0.5 ->", win.quantile(0.5)); // 9

console.log("\n== streaming scenario: sliding 10s window ==");
const stream = new SlidingWindowQuantile(10_000);
let id = 0;
for (let t = 0; t <= 30_000; t += 2_000) {
  // In-order event plus an out-of-order straggler 3s behind.
  stream.add({ id: `e${id++}`, timestamp: t, value: (t / 1_000) * 7 % 100 });
  stream.add({ id: `e${id++}`, timestamp: t - 3_000, value: (t / 1_000) * 3 % 100 });
  if (stream.size > 0) {
    console.log(
      `t=${String(t).padStart(6)}ms  n=${stream.size}  ` +
        `p50=${stream.quantile(0.5)}  p90=${stream.quantile(0.9)}  p99=${stream.quantile(0.99)}`,
    );
  }
}
