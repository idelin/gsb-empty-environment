/**
 * Demo: 5-node Multi-Paxos cluster surviving a network partition.
 *
 * Timeline:
 *  1. Node 0 wins leadership and commits entries via the lease fast path
 *     (Phase 2 only — no per-write Prepare).
 *  2. A partition isolates {0,1} from {2,3,4} mid-replication, leaving:
 *       - slot 3 accepted by a majority {0,1,2} but never committed;
 *       - slot 2 accepted only by the minority {0,1} ("dirty" entry).
 *  3. The lease expires; node 2 wins Phase 1 in the majority partition,
 *     fills the hole at slot 2 with a noop, re-proposes the already-chosen
 *     value at slot 3, and commits both.
 *  4. The partition heals; higher-ballot Accepts safely overwrite the old
 *     leader's dirty minority log. All 5 nodes converge, zero leaks.
 */
import { Cluster } from "./src/cluster.ts";
import { NOOP_VALUE } from "./src/types.ts";

const cluster = new Cluster(5, 1000);
const net = cluster.net;
const log = (msg: string) => console.log(`[t=${cluster.now()}] ${msg}`);

// --- 1. Election + lease fast-path commits --------------------------------
const leader0 = cluster.elect(0);
log(`node 0 elected (lease ${cluster.leaseMs}ms); fast path = Phase 2 only`);
cluster.propose(0, "set x=1"); // slot 0
cluster.propose(0, "set y=2"); // slot 1
log(`node 0 committed: ${JSON.stringify(cluster.nodes[0].appliedValues())}`);

// --- 2. Partition mid-replication ------------------------------------------
// slot 2 ("dirty") is delivered to {0,1} only, then the cut drops the rest.
cluster.partition([0, 1], [2, 3, 4]);
leader0.propose("dirty"); // slot 2: minority {0,1} only
cluster.flush();

// slot 3 ("set z=3") must reach a majority: heal, replicate, then re-cut
// before the commit broadcast lands (in-flight commits are dropped).
cluster.heal();
leader0.propose("set z=3"); // enqueues 5 Accepts for slot 3
net.flushN(10);             // deliver Accepts + Accepteds, commits still queued
net.dropPending();          // commit broadcast lost to the re-partition
cluster.partition([0, 1], [2, 3, 4]);
log(`cut {0,1}|{2,3,4}: slot2=dirty on {0,1}, slot3="set z=3" accepted by {0,1,2}, nothing committed`);

// The isolated old leader cannot commit anything new.
leader0.propose("lost-write");
cluster.flush();
log(`old leader isolated: commitIndex=${cluster.nodes[0].commitIndex} (cannot advance)`);

// --- 3. Lease expiry → new leader repairs via Phase 1 ----------------------
cluster.advance(1500); // lease expires on every acceptor
const leader2 = cluster.elect(2);
log(`node 2 elected; Phase 1 repaired slots, commitIndex=${leader2.commitIndex}`);
log(`node 2 applied: ${JSON.stringify(leader2.appliedValues())}`);

// --- 4. Heal → safe overwrite of the minority dirty log --------------------
cluster.heal();
leader2.syncFollowers();
cluster.flush();
cluster.propose(2, "set w=4"); // slot 4

const logs = cluster.appliedLogs();
for (let i = 0; i < logs.length; i++) log(`node ${i} applied: ${JSON.stringify(logs[i])}`);

const converged = logs.every((l) => JSON.stringify(l) === JSON.stringify(logs[0]));
const checks: Array<[string, boolean]> = [
  ["all 5 nodes converged", converged],
  ["hole at slot 2 filled with noop", logs[0][2] === NOOP_VALUE],
  ["majority-chosen value preserved at slot 3", logs[0][3] === "set z=3"],
  ["minority dirty value overwritten", !logs[0].includes("dirty")],
  ["isolated old leader never committed 'lost-write'", !logs[0].includes("lost-write")],
];
for (const [name, ok] of checks) log(`${ok ? "PASS" : "FAIL"}: ${name}`);

// --- Leak check -------------------------------------------------------------
cluster.dispose();
const outstanding = cluster.outstandingEntries();
log(`pooled LogEntry outstanding after dispose: ${outstanding}`);
if (checks.some(([, ok]) => !ok) || outstanding !== 0) process.exit(1);
log("demo OK");
