import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster, Client } from '../src/cluster.ts';
import type { AcceptReply, Command } from '../src/node.ts';
import { assertNoLeaks, sleep } from './helpers.ts';

test('single leader commits client values with a contiguous watermark', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 1000 });
  const client = new Client('c1', cluster);

  const s1 = await client.submit('A');
  const s2 = await client.submit('B');
  const s3 = await client.submit('C');
  assert.deepEqual([s1, s2, s3], [1, 2, 3]);

  await cluster.settle();
  for (const n of cluster.nodes) {
    assert.equal(n.commitIndex, 3, `node ${n.id} watermark`);
    assert.deepEqual(
      n.committedValues().map((v) => v?.data),
      ['A', 'B', 'C'],
      `node ${n.id} committed prefix`,
    );
  }
  await assertNoLeaks(cluster);
});

test('leader lease bypasses Phase 1 for consecutive entries', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 1000 });
  const leader = cluster.node(1);
  assert.equal(await leader.campaign(), true);

  const prepareAfterElection = leader.stats.prepareSent;
  assert.ok(prepareAfterElection > 0);

  for (let i = 0; i < 5; i++) {
    await leader.propose({ clientId: 'c', seq: i, data: `v${i}` });
  }
  assert.equal(
    leader.stats.prepareSent,
    prepareAfterElection,
    'no Prepare may be sent while the lease is valid',
  );
  assert.ok(leader.stats.acceptSent > 0, 'Phase 2 still runs per entry');
  assert.equal(leader.commitIndex, 5);
  await assertNoLeaks(cluster);
});

test('lease expiry forces a fresh Phase 1 before the next write', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 60 });
  const leader = cluster.node(1);
  assert.equal(await leader.campaign(), true);
  await leader.propose({ clientId: 'c', seq: 0, data: 'x' });

  const campaigns = leader.stats.campaigns;
  const prepareSent = leader.stats.prepareSent;
  await sleep(120); // let the lease expire

  await leader.propose({ clientId: 'c', seq: 1, data: 'y' });
  assert.equal(leader.stats.campaigns, campaigns + 1, 're-election after expiry');
  assert.ok(leader.stats.prepareSent > prepareSent, 'Phase 1 ran again');
  assert.equal(leader.commitIndex, 2);
  await assertNoLeaks(cluster);
});

test('retried command is committed exactly once (idempotency)', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 1000 });
  const leader = cluster.node(1);
  await leader.campaign();

  const cmd: Command = { clientId: 'c', seq: 7, data: 'exactly-once' };
  const slot1 = await leader.propose(cmd);
  // Simulate a client retry after a lost response.
  const slot2 = await leader.propose(cmd);
  assert.equal(slot1, slot2);

  await cluster.settle();
  const committed = leader.committedValues().filter((v) => v !== null);
  assert.equal(committed.length, 1);
  assert.equal(committed[0]!.data, 'exactly-once');
  await assertNoLeaks(cluster);
});

test('acceptor rejects stale ballots after promising a higher one', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 1000 });
  const n1 = cluster.node(1);
  const n2 = cluster.node(2);
  await n1.campaign();
  const staleBallot = n1.currentBallot;
  await n2.campaign(); // wins with a higher ballot

  const reply = await cluster.net.call<AcceptReply>(1, 3, {
    type: 'accept',
    slot: 9,
    ballot: staleBallot,
    value: { clientId: 'c', seq: 0, data: 'stale' },
  });
  assert.equal(reply.ok, false, 'stale accept must be rejected');
  assert.equal(cluster.node(3).entryAt(9), undefined);
  await assertNoLeaks(cluster);
});

test('higher Prepare demotes an incumbent leader', async () => {
  const cluster = new Cluster({ size: 5, leaseMs: 1000 });
  const n1 = cluster.node(1);
  const n2 = cluster.node(2);
  await n1.campaign();
  assert.ok(n1.isLeader);
  await n2.campaign();
  await cluster.settle();
  assert.equal(n1.isLeader, false, 'old leader steps down on higher promise');
  assert.ok(n2.isLeader);
  await assertNoLeaks(cluster);
});
