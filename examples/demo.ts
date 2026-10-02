/**
 * Runnable demo of the acceptance scenario:
 *   term 1: 3-node cluster commits 3 entries (leader C)
 *   C is isolated; A/B move to term 2 and commit 4 more entries
 *   C rejoins, a higher-term heartbeat forces rollback of its
 *   conflicting uncommitted entries; committed entries survive.
 *
 * Run: node examples/demo.ts   (or: npm run demo)
 */
import { Cluster } from "../src/sim.ts";
import type { RaftNode } from "../src/raft.ts";

const cluster = new Cluster([
  { id: "C", electionTimeout: 10, heartbeatInterval: 3 },
  { id: "A", electionTimeout: 15, heartbeatInterval: 3 },
  { id: "B", electionTimeout: 20, heartbeatInterval: 3 },
]);
const [a, b, c] = [cluster.node("A"), cluster.node("B"), cluster.node("C")];

function show(title: string): void {
  console.log(`\n=== ${title} (t=${cluster.time}) ===`);
  for (const n of [a, b, c]) {
    const log = n.log.map((e) => `${e.command}@t${e.term}`).join(" ");
    console.log(
      `  ${n.id}: role=${n.role.padEnd(8)} term=${n.currentTerm} commit=${n.commitIndex} log=[${log}]`,
    );
  }
}

function leader(): RaftNode {
  const l = [a, b, c].find((n) => n.role === "leader");
  if (!l) throw new Error("no leader");
  return l;
}

cluster.tick(10);
show("term 1: C wins pre-vote + election");

c.propose("e1");
c.propose("e2");
c.propose("e3");
cluster.flush();
show("term 1: 3 entries committed on all nodes");

cluster.isolate("C");
c.propose("y4"); // stale leader appends but cannot commit
c.propose("y5");
cluster.flush();
show("C isolated: stale leader holds uncommitted y4,y5");

cluster.tick(20); // B's election timeout -> pre-vote -> term 2
show("A/B elect B for term 2 (pre-vote granted by A)");

b.propose("e4");
b.propose("e5");
b.propose("e6");
b.propose("e7");
cluster.flush();
show("term 2: majority A/B commits 4 more entries");

cluster.healAll();
cluster.tick(3); // B's heartbeat (term 2) reaches C
show("C healed: high-term heartbeat forces rollback of y4,y5");

cluster.tick(50);
show("steady state: cluster stable, no term disruption");

const ok =
  leader().id === "B" &&
  c.commitIndex === 7 &&
  JSON.stringify(c.log) === JSON.stringify(b.log) &&
  c.log.slice(0, 3).every((e, i) => e.command === `e${i + 1}`);
console.log(`\nacceptance checks: ${ok ? "PASS" : "FAIL"}`);
process.exit(ok ? 0 : 1);
