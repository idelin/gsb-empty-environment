/**
 * Acceptance scenario demo:
 *
 *   root 100MB/s
 *   ├── deptA 40MB/s (business line bizA)
 *   └── deptB 60MB/s (business line bizB)
 *
 * Phase 1 (t < 2s):  A uses only 10MB/s -> B borrows the idle 30 and bursts to 90.
 * Phase 2 (t >= 2s): A's traffic recovers to 40MB/s -> B is smoothly
 *                    suppressed back to its guaranteed 60.
 */
import { HtbAllocator } from "../src/index.ts";

const htb = new HtbAllocator(
  {
    name: "root",
    rate: 100,
    children: [
      { name: "deptA", rate: 40, children: [{ name: "bizA", rate: 40 }] },
      { name: "deptB", rate: 60, children: [{ name: "bizB", rate: 60 }] },
    ],
  },
  { attackTau: 0.2, releaseTau: 1.0 },
);

const dt = 0.2;
const totalTime = 8;
const recoveryTime = 2;

const fmt = (n: number): string => n.toFixed(1).padStart(6);
console.log(
  "  t(s) | demandA allocA | demandB allocB |  total",
);
console.log("-------+----------------+----------------+--------");

for (let t = 0; t <= totalTime + 1e-9; t += dt) {
  const demandA = t < recoveryTime ? 10 : 40;
  htb.setDemand("bizA", demandA);
  htb.setDemand("bizB", 100); // B always wants as much as it can get
  htb.tick(dt);

  const allocA = htb.allocOf("bizA");
  const allocB = htb.allocOf("bizB");
  const marker = Math.abs(t - recoveryTime) < 1e-9 ? "  <-- A recovers" : "";
  console.log(
    `${fmt(t)} | ${fmt(demandA)} ${fmt(allocA)} | ${fmt(100)} ${fmt(allocB)} | ${fmt(allocA + allocB)}${marker}`,
  );
}

console.log("\nPhase 1: B borrows A's idle 30MB/s and bursts 60 -> 90.");
console.log("Phase 2: A recovers; B is smoothly suppressed back to 60 (EWMA, tau=1s).");
