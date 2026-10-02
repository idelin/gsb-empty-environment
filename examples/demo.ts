/**
 * Runnable demo of the acceptance scenario:
 *   root 100MB, dept A 40MB, dept B 60MB (ceil 90MB).
 *   Phase 1: A uses only 10MB -> B borrows up to 90MB.
 *   Phase 2: A recovers to 40MB -> B is smoothly pressed back to 60MB.
 *
 * Run: npm run demo   (requires Node >= 22.6)
 */
import { HtbAllocator } from "../src/htb.ts";

const htb = new HtbAllocator(
  {
    name: "root",
    rate: 100,
    children: [
      { name: "A", rate: 40 },
      { name: "B", rate: 60, ceil: 90 },
    ],
  },
  { reclaimAlpha: 0.5 },
);

function bar(mb: number): string {
  return "#".repeat(Math.round(mb / 2)).padEnd(50, ".");
}

function print(tick: number, snap: Map<string, number>): void {
  const a = snap.get("A")!;
  const b = snap.get("B")!;
  console.log(
    `tick ${String(tick).padStart(2)} | A ${a.toFixed(2).padStart(6)}MB ${bar(a)}` +
      ` | B ${b.toFixed(2).padStart(6)}MB ${bar(b)} | total ${(a + b).toFixed(2)}MB`,
  );
}

function run(ticks: number, startTick: number): number {
  for (let i = 0; i < ticks; i++) {
    print(startTick + i, htb.tick());
  }
  return startTick + ticks;
}

console.log("Phase 1: A demands 10MB, B demands 100MB (B may borrow up to ceil=90MB)");
htb.setDemand(["A"], 10);
htb.setDemand(["B"], 100);
let tick = run(4, 0);

console.log("\nPhase 2: A's traffic recovers to 40MB -> B is smoothly reclaimed to 60MB");
htb.setDemand(["A"], 40);
run(10, tick);
