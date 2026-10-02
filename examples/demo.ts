import { CausalBroadcast } from "../src/causalBroadcast.ts";
import type { CausalMessage } from "../src/causalBroadcast.ts";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

function fmt(clock: Record<string, number>): string {
  return JSON.stringify(clock);
}

/**
 * Scenario 1 (acceptance boundary, deterministic):
 * M1 causally depends on M0, but the network delivers M1 first.
 * The middleware must park M1 and cascade-deliver it once M0 arrives.
 */
function deterministicScenario(): void {
  console.log("=== Scenario 1: out-of-order arrival (M1 before M0) ===");
  const alice = new CausalBroadcast<string>("alice");
  const appLog: string[] = [];
  const bob = new CausalBroadcast<string>("bob", (m) => {
    appLog.push(m.payload);
    console.log(
      `  [bob/app] delivered "${m.payload}" (from ${m.sender}, clock=${fmt(m.clock)})`,
    );
  });

  const m0 = alice.broadcast("M0: deploy schema");
  const m1 = alice.broadcast("M1: insert row"); // causally depends on M0

  console.log(`  [network] jitter! M1 reaches bob first`);
  bob.receive(m1);
  console.log(
    `  [bob] M1 depends on M0 which is missing -> parked (buffered=${bob.bufferedCount}, delivered=${appLog.length})`,
  );

  console.log(`  [network] M0 finally arrives`);
  bob.receive(m0);
  console.log(
    `  [bob] cascade drained buffer (buffered=${bob.bufferedCount})`,
  );

  console.log(`  => delivery order at bob: ${JSON.stringify(appLog)}`);
  console.assert(
    appLog.join() === "M0: deploy schema,M1: insert row",
    "causal order violated!",
  );
  console.log();
}

/**
 * Scenario 2: three nodes over a jittery in-memory network with random
 * delays. Whatever the arrival order, every node must deliver in causal order.
 */
async function jitteryNetworkScenario(): Promise<void> {
  console.log("=== Scenario 2: 3 nodes, random network jitter ===");
  const nodes = new Map<string, CausalBroadcast<string>>();
  const logs = new Map<string, string[]>();

  for (const id of ["A", "B", "C"]) {
    logs.set(id, []);
    nodes.set(
      id,
      new CausalBroadcast<string>(id, (m) =>
        logs.get(id)!.push(`${m.sender}:${m.payload}`),
      ),
    );
  }

  const send = (msg: CausalMessage<string>, to: string): void => {
    const delay = Math.floor(Math.random() * 60); // simulated jitter
    setTimeout(() => nodes.get(to)!.receive(msg), delay);
  };

  const broadcast = (from: string, payload: string): void => {
    const msg = nodes.get(from)!.broadcast(payload);
    for (const to of nodes.keys()) if (to !== from) send(msg, to);
  };

  broadcast("A", "a1"); // A's first message
  broadcast("B", "b1"); // concurrent with a1
  // C reacts to a1 -> c1 causally depends on a1
  const a1 = nodes.get("A")!.delivered[0];
  nodes.get("C")!.receive(a1); // C sees a1 in order, then responds
  broadcast("C", "c1-after-a1");
  broadcast("A", "a2"); // a2 causally depends on a1

  await sleep(300); // let the jittery network settle

  for (const [id, log] of logs) {
    console.log(`  [${id}/app] delivery order: ${JSON.stringify(log)}`);
  }

  // Verify causal order at every node: a1 must precede c1-after-a1 and a2.
  for (const [id, log] of logs) {
    const idx = (s: string) => log.findIndex((m) => m.endsWith(s));
    console.assert(idx("a1") < idx("c1-after-a1"), `${id}: a1 !< c1`);
    console.assert(idx("a1") < idx("a2"), `${id}: a1 !< a2`);
  }
  console.log("  => causal order preserved at all nodes");
}

deterministicScenario();
await jitteryNetworkScenario();
