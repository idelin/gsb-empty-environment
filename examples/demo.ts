import { CapacityAwareDistributor } from "../src/distributor.ts";

const KEY_COUNT = 20_000;
const keys = Array.from({ length: KEY_COUNT }, (_, i) => `request-${i}`);

function show(d: CapacityAwareDistributor, title: string): void {
  const counts = new Map<string, number>();
  for (const key of keys) {
    const id = d.route(key);
    counts.set(id, (counts.get(id) ?? 0) + 1);
  }
  console.log(`\n=== ${title} ===`);
  for (const n of d.snapshot()) {
    const share = (((counts.get(n.id) ?? 0) / KEY_COUNT) * 100).toFixed(1);
    console.log(
      `node ${n.id}  cpu=${(n.metrics.cpu * 100).toFixed(0)}%  ` +
        `health=${n.healthFactor.toFixed(2)}  weight=${n.weight}  traffic=${share}%`,
    );
  }
}

const d = new CapacityAwareDistributor();
d.addNode("A", { cpu: 0.3, memory: 0.3 });
d.addNode("B", { cpu: 0.3, memory: 0.3 });
d.addNode("C", { cpu: 0.3, memory: 0.3 });
d.runCycle();
show(d, "cycle 1: all nodes healthy (cpu 30%)");

console.log("\n>>> node A cpu spikes 30% -> 90%");
d.reportMetrics("A", { cpu: 0.9, memory: 0.3 });
d.runCycle();
show(d, "cycle 2: capacity sensor cuts A's weight 100 -> 20, traffic drains to B/C");

console.log("\n>>> node A recovers to cpu 30%");
d.reportMetrics("A", { cpu: 0.3, memory: 0.3 });
d.runCycle();
show(d, "cycle 3: A's weight restored to 100, traffic rebalanced");
