import { Simulation } from "../src/simulation.ts";
import { snapshotTotal, inFlightTotal } from "../src/snapshot.ts";

const seed = Number(process.argv[2] ?? 42);

console.log("=== Chandy-Lamport consistent snapshot demo ===");
console.log(`cluster: A=100, B=100, C=100 (total assets = 300), seed = ${seed}`);
console.log("business: nodes keep firing random transfers; node A initiates snapshot 'snap-1' at t=30\n");

const sim = new Simulation({ balances: { A: 100, B: 100, C: 100 }, seed });
sim.scheduleSnapshotInitiation("A", "snap-1", 30);
sim.runUntil(() => sim.isSnapshotComplete("snap-1"));
const snapshot = sim.collectSnapshot("snap-1");

console.log(`transfers sent: ${sim.transfersSent}, delivered: ${sim.transfersDelivered}, ` +
  `delivered after initiation (business never paused): ${sim.transfersDeliveredAfterInitiation}\n`);

for (const record of snapshot.processes) {
  console.log(`node ${record.processId}: recorded balance = ${record.balance} (at t=${record.recordedAt})`);
  for (const channel of record.channels) {
    if (channel.inFlight.length === 0) {
      console.log(`  channel ${channel.from}->${record.processId}: in-flight = []`);
    } else {
      const msgs = channel.inFlight.map((m) => `#${m.id}(${m.amount})`).join(", ");
      console.log(`  channel ${channel.from}->${record.processId}: in-flight = [${msgs}]`);
    }
  }
}

const balances = snapshot.processes.reduce((n, r) => n + r.balance, 0);
const inFlight = snapshot.processes.reduce((n, r) => n + inFlightTotal(r), 0);
console.log(`\nsum of recorded balances : ${balances}`);
console.log(`sum of in-flight messages: ${inFlight}`);
console.log(`captured total           : ${snapshotTotal(snapshot)}`);
console.log(`initial total            : ${sim.initialTotal}`);
console.log(snapshotTotal(snapshot) === sim.initialTotal
  ? "OK: assets strictly conserved across the consistent cut"
  : "FAIL: conservation violated");
