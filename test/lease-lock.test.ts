import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AcquireTimeoutError,
  FencingTokenError,
  LeaseExpiredError,
  LeaseLockServer,
} from '../src/lease-lock-server.ts';
import { LockClient } from '../src/lock-client.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

test('mutual exclusion: second acquirer waits until release', async () => {
  const server = new LeaseLockServer();
  const clientA = new LockClient(server, 'A');
  const clientB = new LockClient(server, 'B');

  const lockA = await clientA.acquire('res', 5_000);
  assert.equal(server.currentHolder('res'), 'A');

  let bAcquired = false;
  const bPromise = clientB.acquire('res', 5_000).then((h) => {
    bAcquired = true;
    return h;
  });

  await sleep(100);
  assert.equal(bAcquired, false, 'B must block while A holds the lock');

  lockA.release();
  const lockB = await bPromise;
  assert.equal(bAcquired, true);
  assert.equal(server.currentHolder('res'), 'B');
  assert.ok(lockB.fenceToken > lockA.fenceToken, 'fence token increases per grant');
  lockB.release();
});

test('keep-alive heartbeats keep the lease alive beyond its TTL', async () => {
  const server = new LeaseLockServer();
  const clientA = new LockClient(server, 'A');

  const lock = await clientA.acquire('res', 300, { keepAliveIntervalMs: 80 });
  await sleep(1_000); // far beyond the 300ms TTL
  assert.equal(server.isLocked('res'), true, 'lease must survive while heartbeats flow');
  assert.equal(lock.lost, false);
  lock.release();
  assert.equal(server.isLocked('res'), false);
});

test('lease expires when heartbeats stop and lock is handed to the queue', async () => {
  const server = new LeaseLockServer();
  const clientA = new LockClient(server, 'A');
  const clientB = new LockClient(server, 'B');

  const lockA = await clientA.acquire('res', 400, { keepAliveIntervalMs: 100 });
  const bPromise = clientB.acquire('res', 400);

  await sleep(150);
  lockA.suspend(); // node A "crashes": heartbeats stop
  assert.equal(server.isLocked('res'), true, 'lock still held until the lease TTL elapses');

  const lockB = await bPromise; // granted once the lease expires
  assert.equal(server.currentHolder('res'), 'B');
  assert.equal(lockA.lost, false, 'A does not know yet - it is frozen');

  await lockA.resume(); // A comes back and discovers the loss
  assert.equal(lockA.lost, true);
  lockB.release();
});

test('fencing token rejects writes from a stale holder, even after the new holder releases', async () => {
  const server = new LeaseLockServer();
  const clientA = new LockClient(server, 'A');
  const clientB = new LockClient(server, 'B');

  const lockA = await clientA.acquire('res', 200, { keepAlive: false });
  const bPromise = clientB.acquire('res', 200, { keepAlive: false });
  const lockB = await bPromise; // A's lease expires, B takes over

  lockB.write('B-data');
  assert.equal(server.read('res'), 'B-data');
  lockB.release();

  assert.throws(() => lockA.write('A-stale-data'), FencingTokenError);
  assert.equal(server.read('res'), 'B-data', 'stale write must not corrupt the resource');
});

test('renew on an expired lease throws LeaseExpiredError', async () => {
  const server = new LeaseLockServer();
  const clientA = new LockClient(server, 'A');

  const lock = await clientA.acquire('res', 150, { keepAlive: false });
  await sleep(300);
  assert.throws(() => server.renew(lock.grant.leaseId), LeaseExpiredError);
  assert.equal(lock.release(), false);
});

test('queued acquire times out when the holder never releases', async () => {
  const server = new LeaseLockServer();
  const clientA = new LockClient(server, 'A');
  const clientB = new LockClient(server, 'B');

  const lockA = await clientA.acquire('res', 5_000);
  await assert.rejects(
    clientB.acquire('res', 5_000, { waitTimeoutMs: 200 }),
    AcquireTimeoutError,
  );
  lockA.release();
});

test('ACCEPTANCE: A hangs, lease times out after 5s, B acquires, A is fenced out on recovery', async (t) => {
  const TTL_MS = 5_000;
  const server = new LeaseLockServer();
  const clientA = new LockClient(server, 'node-A');
  const clientB = new LockClient(server, 'node-B');

  // 1. Node A acquires the lock and starts heartbeating.
  const lockA = await clientA.acquire('shared-resource', TTL_MS, { keepAliveIntervalMs: 1_000 });
  assert.equal(lockA.fenceToken, 1);
  lockA.write('A-initial');

  // 2. Node B queues up for the same lock.
  const bPromise = clientB.acquire('shared-resource', TTL_MS);

  // 3. Node A fake-hangs: heartbeats stop after ~1 successful renewal.
  await sleep(1_200);
  lockA.suspend();
  const hangAt = Date.now();
  t.diagnostic(`node-A suspended at t=${hangAt}`);

  // 4. Lease naturally times out ~5s after A's last heartbeat; B is granted.
  const lockB = await bPromise;
  const waitedMs = Date.now() - hangAt;
  t.diagnostic(`node-B acquired after waiting ${waitedMs}ms past A's hang`);
  assert.equal(server.currentHolder('shared-resource'), 'node-B');
  assert.equal(lockB.fenceToken, 2, 'B gets a strictly larger fence token');
  assert.ok(
    waitedMs >= 3_500 && waitedMs <= TTL_MS + 1_000,
    `B should acquire only after the 5s lease lapses (waited ${waitedMs}ms since hang)`,
  );

  // 5. A recovers and tries to write with its old fence token -> rejected.
  lockA.resume();
  assert.equal(lockA.lost, true, 'A discovers its lease is gone');
  assert.throws(() => lockA.write('A-stale-write'), FencingTokenError);

  // 6. B, the legitimate holder, writes successfully.
  lockB.write('B-data');
  assert.equal(server.read('shared-resource'), 'B-data');
  lockB.release();
});
