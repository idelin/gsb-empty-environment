/**
 * End-to-end demo of the dual-ring hot-standby routing controller.
 *
 * Scenario (the acceptance boundary):
 *   1. Active ring (12 nodes / 3 zones) serves steady traffic.
 *   2. A scale-in change (remove 3 nodes) is injected into the STANDBY ring.
 *   3. Standby prewarms: it pre-computes the key-migration impact.
 *   4. The controller performs an atomic pointer flip; traffic cutover is
 *      instantaneous with zero transient misrouting.
 */

import { performance } from "node:perf_hooks";
import { DualRingController } from "../src/controller.ts";
import { HashRing, type RingNode } from "../src/hash-ring.ts";

const ZONES = ["zone-a", "zone-b", "zone-c"];
const nodes: RingNode[] = Array.from({ length: 12 }, (_, i) => ({
  id: `cache-${String(i).padStart(2, "0")}`,
  zone: ZONES[i % ZONES.length],
}));
const retiring = ["cache-03", "cache-07", "cache-11"];
const TRAFFIC = 200_000;

const line = (label: string) => console.log(`\n=== ${label} ===`);

line("1. Boot: active ring serving steady traffic");
const controller = new DualRingController(nodes);
console.log(
  `active ring: ${controller.activeNodeIds().length} nodes, ` +
    `version=${controller.activeVersion}, standbyReady=${controller.standbyReady}`,
);

const keys = Array.from({ length: TRAFFIC }, (_, i) => `session:${i}`);
const t0 = performance.now();
let routed = 0;
for (const key of keys) {
  controller.route(key);
  routed++;
}
const routeMs = performance.now() - t0;
console.log(
  `routed ${routed.toLocaleString()} keys in ${routeMs.toFixed(1)}ms ` +
    `(${(routed / routeMs / 1000).toFixed(1)}M lookups/s)`,
);

line("2. Inject scale-in into the STANDBY ring (active untouched)");
controller.syncStandby(retiring.map((nodeId) => ({ type: "remove", nodeId }) as const));
console.log(`retiring nodes: ${retiring.join(", ")}`);
console.log(
  `active still on version=${controller.activeVersion}, ` +
    `standby on version=${controller.standbyVersion}, standbyReady=${controller.standbyReady}`,
);

line("3. Prewarm: pre-compute key-migration impact");
const plan = controller.prewarm(keys);
console.log(`migration plan:`);
console.log(`  removed nodes      : ${plan.removedNodes.join(", ")}`);
console.log(
  `  vnode ranges moving: ${plan.movedVnodes}/${plan.totalVnodes} ` +
    `(${(plan.vnodeMoveRatio * 100).toFixed(2)}%)`,
);
console.log(
  `  keys migrating     : ${plan.movedKeys.toLocaleString()}/${plan.sampledKeys.toLocaleString()} ` +
    `(${(plan.keyMoveRatio * 100).toFixed(2)}% of sampled traffic)`,
);
console.log(`standbyReady=${controller.standbyReady}`);

line("4. Atomic pointer flip under live traffic");
const reference = new HashRing(nodes.filter((n) => !retiring.includes(n.id)));
let misrouted = 0;
let checked = 0;
let flip: ReturnType<DualRingController["flip"]> | null = null;
for (let i = 0; i < keys.length; i++) {
  if (i === TRAFFIC / 2) flip = controller.flip();
  const owner = controller.route(keys[i]);
  if (flip !== null) {
    checked++;
    if (owner !== reference.lookup(keys[i])) misrouted++;
  }
}
console.log(`flip duration        : ${flip!.durationMs.toFixed(3)}ms (single pointer swap)`);
console.log(`post-flip checks     : ${checked.toLocaleString()} lookups verified`);
console.log(`transient misrouting : ${misrouted} (zero-tolerance)`);
console.log(
  `active ring now      : ${controller.activeNodeIds().length} nodes, ` +
    `version=${controller.activeVersion}, flipCount=${controller.flipCount}`,
);

line("Result");
if (misrouted === 0 && flip!.durationMs < 10) {
  console.log("OK - smooth cutover: millisecond-level flip, zero routing errors.");
} else {
  console.error("FAILED - misrouting or slow flip detected");
  process.exitCode = 1;
}
