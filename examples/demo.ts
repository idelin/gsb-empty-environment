/**
 * Runnable demo of the acceptance scenario:
 *
 *   - The sender injects a 1 MiB "file" as fast as possible.
 *   - The receiver consumes slowly (4 KiB / 25 ms) on a 64 KiB buffer.
 *   - Mid-transfer the receiver pushes a window-shrink notification
 *     (64 KiB -> 12 KiB), and later grows it back.
 *   - The sender parks whenever in-flight bytes fill the advertised window,
 *     probes while the window is zero, and resumes on window-growth beacons.
 *
 * Run: npm run demo
 */
import { createHash } from 'node:crypto';
import { Endpoint } from '../src/endpoint.ts';
import { Link } from '../src/link.ts';

const KiB = 1024;
const MiB = 1024 * KiB;
const t0 = Date.now();

function log(...args: unknown[]): void {
  const t = `${Date.now() - t0}`.padStart(5);
  console.log(`[t+${t}ms]`, ...args);
}

function makePayload(bytes: number, seed = 0xc0ffee): Buffer {
  const buf = Buffer.alloc(bytes);
  let x = seed >>> 0;
  for (let i = 0; i < bytes; i++) {
    x = (Math.imul(x, 1664525) + 1013904223) >>> 0;
    buf[i] = (x >>> 16) & 0xff;
  }
  return buf;
}

const sha256 = (b: Buffer) => createHash('sha256').update(b).digest('hex');

// --- topology -------------------------------------------------------------
const sender = new Endpoint({ name: 'sender', capacity: 256 * KiB, probeIntervalMs: 40 });
const receiver = new Endpoint({ name: 'receiver', capacity: 64 * KiB, probeIntervalMs: 40 });
const link = new Link(2); // 2 ms one-way latency
link.connect(sender, receiver);

// --- receiver: deliberately slow consumer ----------------------------------
receiver.receiver.startAutoConsume({ chunkBytes: 4 * KiB, intervalMs: 25 });
const received: Buffer[] = [];
receiver.receiver.on('data', (chunk: Buffer) => received.push(chunk));
receiver.receiver.on('beacon', (ack: number, window: number) => {
  if (window === 0) log(`  <- BEACON ack=${ack} window=0 (receive buffer full)`);
});

// --- sender: flow-state instrumentation ------------------------------------
sender.sender.on('suspend', (e) =>
  log(`  !! SUSPEND  in-flight=${e.bytesInFlight}B fills window=${e.window}B, queued=${e.queuedBytes}B`),
);
sender.sender.on('resume', (e) =>
  log(`  >> RESUME   window=${e.window}B, in-flight=${e.bytesInFlight}B`),
);
let probeCount = 0;
sender.sender.on('probe', () => {
  probeCount++;
  if (probeCount % 5 === 1) log(`  ?? PROBE #${probeCount}  window is zero, probing peer...`);
});

// --- scenario ---------------------------------------------------------------
const file = makePayload(1 * MiB);
log(`injecting 1 MiB file (sha256=${sha256(file).slice(0, 16)}...)`);
log(`receiver: capacity=64KiB, consumes 4KiB/25ms (slow consumer)`);
sender.send(file);

const shrinkAt = 400;
const growAt = 1200;
setTimeout(() => {
  log(`-- receiver pushes WINDOW-SHRINK notification: 64KiB -> 12KiB`);
  receiver.receiver.setCapacity(12 * KiB);
}, shrinkAt);
setTimeout(() => {
  log(`-- receiver pushes WINDOW-GROW notification: 12KiB -> 64KiB`);
  receiver.receiver.setCapacity(64 * KiB);
}, growAt);

const progress = setInterval(() => {
  log(
    `progress: in-flight=${sender.sender.bytesInFlight}B window=${sender.sender.currentWindow}B ` +
      `pending=${sender.sender.pendingBytes}B recv-buffered=${receiver.receiver.bufferedBytes}B`,
  );
}, 250);

// --- finish -----------------------------------------------------------------
await sender.sender.waitForDrain();
clearInterval(progress);
receiver.receiver.stopAutoConsume();
while (receiver.receiver.bufferedBytes > 0) {
  receiver.receiver.consume(receiver.receiver.bufferedBytes);
}

const ok = sha256(Buffer.concat(received)) === sha256(file);
log(`transfer complete in ${Date.now() - t0}ms`);
log(`sender stats:`, sender.sender.stats);
log(`receiver stats:`, receiver.receiver.stats);
log(`integrity check (sha256): ${ok ? 'PASS' : 'FAIL'}`);

sender.close();
receiver.close();
process.exit(ok ? 0 : 1);
