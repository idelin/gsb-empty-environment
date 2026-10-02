import { FencingTokenError, LeaseLockServer } from '../src/lease-lock-server.ts';
import { LockClient } from '../src/lock-client.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
const t0 = Date.now();
const log = (msg: string) => console.log(`[+${((Date.now() - t0) / 1000).toFixed(1)}s] ${msg}`);

const TTL_MS = 5_000;

const server = new LeaseLockServer();
const nodeA = new LockClient(server, 'node-A');
const nodeB = new LockClient(server, 'node-B');

// 1. Node A acquires the distributed lock with a 5s lease and starts heartbeats.
const lockA = await nodeA.acquire('shared-resource', TTL_MS, { keepAliveIntervalMs: 1_000 });
log(`node-A acquired "shared-resource" (fenceToken=${lockA.fenceToken}, ttl=${TTL_MS}ms), keep-alive every 1s`);
lockA.write('A-initial-data');
log(`node-A wrote "A-initial-data" -> store = ${JSON.stringify(server.read('shared-resource'))}`);

// 2. Node B queues for the same lock.
const bWaiting = nodeB.acquire('shared-resource', TTL_MS);
log('node-B is queued, waiting for the lock...');

// 3. Node A fake-hangs (simulated crash): heartbeats stop.
await sleep(1_200);
lockA.suspend();
log('node-A CRASHED (suspended) - heartbeats stopped, lease will expire 5s after its last heartbeat');

// 4. The lease lapses naturally and the server hands the lock to node B.
const lockB = await bWaiting;
log(`node-B acquired "shared-resource" (fenceToken=${lockB.fenceToken}) after lease timeout`);

// 5. Node A recovers and tries to write with its stale fence token -> rejected.
lockA.resume();
log(`node-A recovered; lease already lost (lockA.lost=${lockA.lost})`);
try {
  lockA.write('A-stale-write');
  log('BUG: stale write was accepted!');
  process.exitCode = 1;
} catch (err) {
  if (err instanceof FencingTokenError) {
    log(`node-A write REJECTED by fencing token: ${err.message}`);
  } else {
    throw err;
  }
}

// 6. Node B, the legitimate holder, writes successfully.
lockB.write('B-data');
log(`node-B wrote "B-data" -> store = ${JSON.stringify(server.read('shared-resource'))}`);
lockB.release();
log('node-B released the lock. Demo finished.');
