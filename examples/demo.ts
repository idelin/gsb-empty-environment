import { SlidingWindowDurationIntegrator } from "../src/SlidingWindowDurationIntegrator.ts";

// Scenario: 100 s sliding window, alarm while temperature > 80 C.
// Temperature rises to 85 C at t=10 s and drops back to 75 C at t=40 s.
const integ = new SlidingWindowDurationIntegrator(100_000, (t) => t > 80);

integ.addSample(0, 75);
integ.addSample(10_000, 85);
integ.addSample(40_000, 75);

console.log("t (s) | over-temp ms in last 100 s");
console.log("------+---------------------------");
for (const t of [50_000, 110_000, 111_000, 120_000, 130_000, 139_000, 140_000, 150_000]) {
  console.log(`${String(t / 1000).padStart(5)} | ${integ.totalActiveMs(t)}`);
}
