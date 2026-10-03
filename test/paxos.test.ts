import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/paxos/cluster.ts';
import { NotLeaderError } from '../src/paxos/node.ts';
import { NOOP_VALUE } from '../src/paxos/types.ts';

const IDS = ['N1', 'N2', 'N3', 'N4', 'N5'];

test('election grants leadership and entries commit through the full cluster', () => {
  const cluster = new Cluster(IDS);
  const leader = cluster.elect('N1');
  assert.equal(leader.getRole(), 'leader');
  assert.equal(cluster.leader()?.id, 'N1');
  assert.ok(leader.hasValidLease());

  cluster.propose('set x=1');
  cluster.propose('set y=2');
  cluster.propose('set z=3');

  assert.equal(leader.getCommitIndex(), 3);
  for (const id of IDS) {
    const node = cluster.node(id);
    assert.equal(node.getCommitIndex(), 3, `${id} commitIndex`);
    assert.deepEqual(
      node.committedEntries().map((e) => e.value),
      ['set x=1', 'set y=2', 'set z=3'],
      `${id} committed values`,
    );
  }
  assert.equal(cluster.network.pending, 0);
  cluster.dispose();
});

test('leader lease fast path skips phase 1 entirely', () => {
  const cluster = new Cluster(IDS, { leaseMs: 1000 });
  cluster.elect('N1');
  cluster.network.resetStats();

  cluster.propose('a');
  cluster.propose('b');
  cluster.propose('c');

  assert.equal(cluster.network.countByType('Prepare'), 0, 'no phase 1 under a valid lease');
  assert.equal(cluster.network.countByType('Promise'), 0);
  // 3 entries x 4 peers.
  assert.equal(cluster.network.countByType('Accept'), 12);
  assert.equal(cluster.network.countByType('Accepted'), 12);
  assert.equal(cluster.node('N1').getCommitIndex(), 3);
  cluster.dispose();
});

test('expired lease forces the leader to re-run phase 1 before committing', () => {
  const cluster = new Cluster(IDS, { leaseMs: 100 });
  const leader = cluster.elect('N1');
  cluster.propose('before-expiry');
  cluster.advanceClock(150);
  assert.equal(leader.hasValidLease(), false);
  cluster.network.resetStats();

  cluster.propose('after-expiry');

  assert.ok(cluster.network.countByType('Prepare') >= 4, 'phase 1 re-executed');
  assert.equal(leader.getRole(), 'leader', 'leader revalidated its mandate');
  assert.equal(leader.getCommitIndex(), 2);
  assert.deepEqual(
    leader.committedEntries().map((e) => e.value),
    ['before-expiry', 'after-expiry'],
  );
  cluster.dispose();
});

test('propose on a follower throws NotLeaderError pointing at the leader', () => {
  const cluster = new Cluster(IDS);
  cluster.elect('N1');
  cluster.propose('warm-up'); // followers learn who the leader is
  const follower = cluster.node('N3');
  assert.throws(
    () => follower.propose('nope'),
    (err: unknown) => err instanceof NotLeaderError && err.knownLeader === 'N1',
  );
  cluster.dispose();
});

test('new leader repairs holes: adopts visible values, fills gaps with no-ops', () => {
  const cluster = new Cluster(IDS);
  const n1 = cluster.elect('N1');
  cluster.propose('v1'); // slot 1, committed everywhere
  cluster.propose('v2'); // slot 2, committed everywhere

  // v3 lands only on the minority side {N1,N2}: dirty and uncommitted.
  cluster.network.partition([['N1', 'N2'], ['N3', 'N4', 'N5']]);
  n1.propose('v3');
  cluster.network.flush();
  assert.equal(n1.getCommitIndex(), 2, 'no majority for slot 3');

  // Heal, then v4 is accepted by everyone but cannot commit past the hole.
  cluster.network.heal();
  n1.propose('v4');
  cluster.network.flush();
  assert.equal(n1.isCommitted(4), true, 'slot 4 majority-accepted');
  assert.equal(n1.getCommitIndex(), 2, 'hole at slot 3 blocks commitIndex (no-hole invariant)');
  assert.equal(cluster.node('N4').entry(3), undefined, 'majority side has a hole at slot 3');
  assert.equal(cluster.node('N4').entry(4)?.value, 'v4');

  // Re-partition so the dirty slot-3 value is invisible to the new quorum.
  cluster.network.partition([['N1', 'N2'], ['N3', 'N4', 'N5']]);
  const n3 = cluster.node('N3');
  n3.campaign();
  cluster.network.flush();
  assert.equal(n3.getRole(), 'leader');

  // Phase 1 repair: slot 3 was invisible -> no-op; slot 4 = v4 -> adopted.
  assert.equal(n3.entry(3)?.value, NOOP_VALUE, 'hole filled with no-op');
  assert.equal(n3.entry(4)?.value, 'v4', 'visible uncommitted value adopted, not overwritten');
  assert.equal(n3.getCommitIndex(), 4, 'commitIndex advances contiguously over repaired slots');
  for (let slot = 1; slot <= 4; slot++) {
    assert.equal(n3.isCommitted(slot), true, `slot ${slot} committed, no holes`);
  }
  cluster.dispose();
});

test('concurrent campaigns yield exactly one leader per term', () => {
  const cluster = new Cluster(IDS);
  const n1 = cluster.node('N1');
  const n4 = cluster.node('N4');
  n1.campaign();
  n4.campaign(); // same term, higher nodeId tie-break
  cluster.network.flush();

  const leaders = IDS.map((id) => cluster.node(id)).filter((n) => n.getRole() === 'leader');
  assert.equal(leaders.length, 1, 'exactly one leader');
  assert.equal(leaders[0].id, 'N4', 'higher ballot (term,nodeId) wins');
  assert.equal(n1.getRole(), 'follower', 'loser concedes to the higher ballot');

  // Retry with a fresh term succeeds.
  n1.campaign();
  cluster.network.flush();
  assert.equal(n1.getRole(), 'leader');
  assert.equal(n1.getCurrentBallot()?.term, 2);
  const leadersNow = IDS.map((id) => cluster.node(id)).filter((n) => n.getRole() === 'leader');
  assert.equal(leadersNow.length, 1);
  cluster.dispose();
});

test('commitIndex never skips an uncommitted slot (hole-free commit engine)', () => {
  const cluster = new Cluster(IDS);
  const n1 = cluster.elect('N1');
  cluster.propose('e1');
  cluster.propose('e2');

  // Slot 3 accepted only on the leader itself.
  cluster.network.partition([['N1'], ['N2', 'N3', 'N4', 'N5']]);
  n1.propose('e3');
  cluster.network.flush();
  cluster.network.heal();

  // Slot 4 commits on a majority while slot 3 is undecided.
  n1.propose('e4');
  cluster.network.flush();

  assert.equal(n1.isCommitted(4), true);
  assert.equal(n1.getCommitIndex(), 2, 'commitIndex stalls at the hole');
  cluster.dispose();
});

test('dispose leaves no listeners, registrations, or queued messages behind', () => {
  const cluster = new Cluster(IDS);
  const leader = cluster.elect('N1');
  leader.on('commit', () => {});
  cluster.propose('x');
  cluster.dispose();

  assert.equal(cluster.network.registeredCount, 0, 'all nodes unregistered');
  assert.equal(cluster.network.pending, 0, 'no queued messages');
  for (const id of IDS) {
    const node = cluster.node(id);
    assert.equal(node.listenerCount('commit'), 0, `${id} has no dangling listeners`);
    assert.equal(node.listenerCount('leader'), 0);
    assert.equal(node.logSize, 0, `${id} log cleared`);
    assert.equal(node.getCommitIndex(), 0);
    assert.equal(node.pendingClientCount, 0);
  }
});
