/**
 * End-to-end demo of the Multi-Paxos commit engine:
 *   1. 5-node cluster elects a leader and commits entries via the lease
 *      fast path (Phase 1 skipped).
 *   2. A network partition isolates the old leader in a 2-node minority;
 *      its dirty entries replicate only to the minority and never commit.
 *   3. The majority elects a new leader (Phase 1), which commits fresh
 *      entries and — after healing — safely overwrites the minority's
 *      dirty log through quorum Accept + catch-up Commit messages.
 *
 * Run:  node examples/demo.ts        (Node >= 22.6, no build step)
 */

import { SimNet } from '../src/simnet.ts';
import { ObjectPool } from '../src/pool.ts';
import { PaxosNode, makeEntry, resetEntry } from '../src/paxos.ts';
import type { LogEntry } from '../src/paxos.ts';

const N = 5;
const net = new SimNet();
const entryPool = new ObjectPool<LogEntry>(makeEntry, resetEntry, 4096, 512);
const nodes: PaxosNode[] = [];
for (let i = 0; i < N; i++) nodes.push(new PaxosNode(i, net, N, 1000, entryPool));

function show(tag: string): void {
  const logs = nodes.map((n) => `n${n.id}[${n.role[0]}]=[${n.committedLog().join(',')}]`);
  console.log(`${tag}\n  ${logs.join('  ')}`);
}

console.log('=== Phase A: election + lease fast path ===');
nodes[0].becomeCandidate();
net.flush();
console.log(`node0 elected (prepareRounds=${nodes[0].stats.prepareRounds})`);
for (const v of ['A', 'B', 'C']) nodes[0].propose(v);
net.flush();
console.log(`3 entries committed via lease fast path (fastPathProposals=${nodes[0].stats.fastPathProposals})`);
show('committed everywhere:');

console.log('\n=== Phase B: partition {0,1} | {2,3,4}, old leader dirties minority ===');
net.partition([0, 1], [2, 3, 4]);
nodes[0].propose('dirty1');
nodes[0].propose('dirty2');
net.flush();
console.log(`old leader commitIndex=${nodes[0].committedIndex} (stuck at 3: minority < quorum)`);
console.log(`node0 slot4='${nodes[0].entryAt(4)?.value}' committed=${nodes[0].entryAt(4)?.committed}`);

console.log('\n=== Phase C: majority elects new leader, Phase 1 repair, new commits ===');
nodes[2].becomeCandidate();
net.flush();
console.log(`node2 role=${nodes[2].role} (prepareRounds=${nodes[2].stats.prepareRounds})`);
nodes[2].propose('N1');
nodes[2].propose('N2');
net.flush();
show('majority commits N1,N2; minority frozen:');

console.log('\n=== Phase D: heal — old leader steps down, dirty log overwritten ===');
net.healAll();
nodes[2].propose('N3');
net.flush();
show('fully converged:');
console.log(`node0 role=${nodes[0].role} stepDowns=${nodes[0].stats.stepDowns}`);
console.log(`node0 slot4='${nodes[0].entryAt(4)?.value}' committed=${nodes[0].entryAt(4)?.committed} (dirty1 safely overwritten)`);

let violations = 0;
for (const n of nodes) violations += n.stats.safetyViolations;
console.log(`\nsafety violations across cluster: ${violations}`);

for (const n of nodes) n.dispose();
entryPool.assertNoLeaks('entryPool');
net.msgPool.assertNoLeaks('msgPool');
console.log(`leak check: PASS (entryPool created=${entryPool.stats.created} reused=${entryPool.stats.reused}, msgPool created=${net.msgPool.stats.created} reused=${net.msgPool.stats.reused})`);
console.log(`network: delivered=${net.delivered} dropped=${net.dropped} (partition drops)`);
