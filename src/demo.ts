import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Scheduler } from "./scheduler.ts";
import { Transport } from "./transport.ts";
import { Coordinator } from "./coordinator.ts";
import { Participant } from "./participant.ts";
import { AuditLog } from "./audit.ts";

const dir = mkdtempSync(join(tmpdir(), "2pc-demo-"));
const scheduler = new Scheduler();
const transport = new Transport(scheduler, 1);
const audit = new AuditLog(join(dir, "audit.log"));
const trace: string[] = [];
const log = (line: string) => {
  trace.push(line);
  console.log(line);
};

const participants = ["p1", "p2", "p3"].map(
  (id) => new Participant(id, transport, join(dir, `${id}.wal`)),
);
const [p1, p2, p3] = participants;

let coord = new Coordinator(
  "coord",
  transport,
  scheduler,
  join(dir, "coord.wal"),
  audit,
  { prepareTimeout: 10, retryInterval: 5, maxRetries: 3, onEvent: log },
);

console.log("=== Phase 1: all participants vote YES, decision COMMIT is logged ===");
coord.beginTransaction("txn-1", ["p1", "p2", "p3"]);

// Run until the DECISION is durable in the WAL but crash BEFORE the
// commit messages are delivered (votes arrive at t=2, decision messages
// would be delivered at t=3, so crashing at t=2.5 loses them in flight).
scheduler.schedule(2.5, () => coord.crash());
scheduler.run();

console.log(`\ncoordinator state after crash: ${coord.stateOf("txn-1")}`);
console.log("participants are PREPARED, holding locks, waiting...\n");

console.log("=== Phase 2: coordinator restarts, replays WAL, queries participants ===");
console.log("=== (p3 stays crashed to force the pending-audit path) ===");
p3.crash();
coord = new Coordinator(
  "coord",
  transport,
  scheduler,
  join(dir, "coord.wal"),
  audit,
  { prepareTimeout: 10, retryInterval: 5, maxRetries: 3, onEvent: log },
);
coord.recover();
scheduler.run();

console.log(`\ncoordinator state: ${coord.stateOf("txn-1")} (p3 unresolved)`);
console.log("audit log:", audit.events());

console.log("\n=== Phase 3: p3 comes back; a recovery pass finishes the pending txn ===");
p3.restart();
coord.recover();
scheduler.run();

console.log(`\nfinal coordinator state: ${coord.stateOf("txn-1")}`);
for (const p of participants) {
  console.log(`${p.id} status: ${p.statusOf("txn-1")}, applied:`, p.appliedLog);
}
console.log("audit log:", audit.events());
console.log(`\nartifacts dir: ${dir}`);
