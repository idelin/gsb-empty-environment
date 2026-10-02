import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  decodeFrame,
  encodeFrame,
  FrameType,
  EMPTY_PAYLOAD,
  type Frame,
} from '../src/frame.ts';
import { FlowControlledReceiver } from '../src/receiver.ts';
import { SlidingWindowSender } from '../src/sender.ts';
import { Endpoint } from '../src/endpoint.ts';
import { Link } from '../src/link.ts';

const KiB = 1024;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/** Deterministic pseudo-random payload (LCG), so integrity is verifiable. */
function makePayload(bytes: number, seed = 0x12345678): Buffer {
  const buf = Buffer.alloc(bytes);
  let x = seed >>> 0;
  for (let i = 0; i < bytes; i++) {
    x = (Math.imul(x, 1664525) + 1013904223) >>> 0;
    buf[i] = (x >>> 16) & 0xff;
  }
  return buf;
}

function sha256(buf: Buffer): string {
  return createHash('sha256').update(buf).digest('hex');
}

function collect(receiver: FlowControlledReceiver): Buffer[] {
  const parts: Buffer[] = [];
  receiver.on('data', (chunk: Buffer) => parts.push(chunk));
  return parts;
}

/** Pull whatever is still buffered so 'data' collectors see every byte. */
function drainReceiver(receiver: FlowControlledReceiver): void {
  receiver.stopAutoConsume();
  while (receiver.bufferedBytes > 0) receiver.consume(receiver.bufferedBytes);
}

/**
 * Watches one direction of a link and asserts the core protocol invariant:
 * a DATA frame may only be sent within the window edge implied by the most
 * recent beacon the sender has received (seq + len <= ack + window).
 */
function makeWindowInvariantMonitor(link: Link, dataFrom: Endpoint, beaconFrom: Endpoint) {
  let ack = 0;
  let window = 0;
  const violations: string[] = [];
  link.addObserver((frame: Frame, from: Endpoint) => {
    if (from === beaconFrom) {
      ack = frame.ack;
      window = frame.window;
    } else if (from === dataFrom && frame.type === FrameType.DATA) {
      const edge = ack + window;
      const end = frame.seq + frame.payload.length;
      if (end > edge) {
        violations.push(
          `DATA [${frame.seq}, ${end}) exceeds window edge ${edge} (ack=${ack}, window=${window})`,
        );
      }
    }
  });
  return violations;
}

test('frame codec round-trips DATA and beacon frames', () => {
  const data: Frame = {
    type: FrameType.DATA,
    seq: 4096,
    ack: 128,
    window: 65535,
    payload: makePayload(1000),
  };
  assert.deepEqual(decodeFrame(encodeFrame(data)), data);

  const beacon: Frame = { type: FrameType.ACK, seq: 0, ack: 8192, window: 0, payload: EMPTY_PAYLOAD };
  const decoded = decodeFrame(encodeFrame(beacon));
  assert.equal(decoded.type, FrameType.ACK);
  assert.equal(decoded.ack, 8192);
  assert.equal(decoded.window, 0);
  assert.equal(decoded.payload.length, 0);

  assert.throws(() => decodeFrame(Buffer.alloc(4)), /too short/);
});

test('receiver beacons window = capacity - buffered, grows back on consume', () => {
  const beacons: Array<{ ack: number; window: number }> = [];
  const receiver = new FlowControlledReceiver({
    capacity: 64 * KiB,
    onBeacon: (ack, window) => beacons.push({ ack, window }),
  });

  receiver.handleData(0, makePayload(16 * KiB));
  assert.deepEqual(beacons.at(-1), { ack: 16 * KiB, window: 48 * KiB });

  receiver.handleData(16 * KiB, makePayload(32 * KiB));
  assert.deepEqual(beacons.at(-1), { ack: 48 * KiB, window: 16 * KiB });

  const out = receiver.consume(40 * KiB);
  assert.equal(out.length, 40 * KiB);
  assert.deepEqual(beacons.at(-1), { ack: 48 * KiB, window: 56 * KiB });

  // Window-shrink notification is pushed immediately.
  receiver.setCapacity(8 * KiB);
  assert.deepEqual(beacons.at(-1), { ack: 48 * KiB, window: 0 });

  // In-flight frame sent against the pre-shrink beacon (edge = 104 KiB) must
  // still be accepted even though the current window is now zero.
  receiver.handleData(48 * KiB, makePayload(4 * KiB));
  assert.equal(receiver.bufferedBytes, 12 * KiB);
  assert.deepEqual(beacons.at(-1), { ack: 52 * KiB, window: 0 });

  // Defensive: a frame beyond the advertised right edge is dropped.
  receiver.handleData(52 * KiB, makePayload(60 * KiB));
  assert.equal(receiver.stats.outOfWindowDrops, 1);
  assert.equal(receiver.bufferedBytes, 12 * KiB);
});

test('sender suspends when in-flight bytes fill the window, resumes on growth beacon', () => {
  const sent: Frame[] = [];
  const sender = new SlidingWindowSender((frame) => sent.push(frame), {
    maxPayloadSize: 4 * KiB,
    probeIntervalMs: 60_000, // keep probes out of this synchronous test
  });
  const events: string[] = [];
  sender.on('suspend', () => events.push('suspend'));
  sender.on('resume', () => events.push('resume'));

  sender.send(makePayload(64 * KiB));
  assert.equal(sent.length, 0, 'window starts closed: nothing sent before first beacon');
  assert.equal(sender.isSuspended, true);
  assert.deepEqual(events, ['suspend']);

  // Beacon opens a 16 KiB window -> exactly 16 KiB goes in flight, then suspend.
  sender.handleBeacon(0, 16 * KiB);
  const inFlight = sent.reduce((n, f) => n + f.payload.length, 0);
  assert.equal(inFlight, 16 * KiB);
  assert.equal(sender.bytesInFlight, 16 * KiB);
  assert.equal(sender.isSuspended, true);
  assert.deepEqual(events, ['suspend'], 'still parked at the window edge');

  // ACK half of it without growing the window -> sender refills to the edge.
  sender.handleBeacon(8 * KiB, 16 * KiB);
  const inFlight2 = sent.reduce((n, f) => n + f.payload.length, 0);
  assert.equal(inFlight2, 24 * KiB);
  assert.equal(sender.bytesInFlight, 16 * KiB);

  // Window-shrink beacon: sender must not send anything further.
  sender.handleBeacon(16 * KiB, 4 * KiB);
  const countAfterShrink = sent.length;
  sender.send(makePayload(4 * KiB));
  assert.equal(sent.length, countAfterShrink, 'no sends while in-flight > shrunk window');

  // Window-grow beacon resumes transmission.
  sender.handleBeacon(16 * KiB, 64 * KiB);
  assert.ok(sent.length > countAfterShrink);
  assert.equal(sender.isSuspended, false);
  assert.deepEqual(events, ['suspend', 'resume']);
  sender.close();
});

test('fast injection vs slow consumer: sender parks at the window edge, integrity holds', async (t) => {
  const a = new Endpoint({ name: 'A', capacity: 256 * KiB, probeIntervalMs: 10 });
  const b = new Endpoint({ name: 'B', capacity: 32 * KiB, probeIntervalMs: 10 });
  const link = new Link(1);
  link.connect(a, b);
  t.after(() => {
    a.close();
    b.close();
  });
  const violations = makeWindowInvariantMonitor(link, a, b);

  // Slow consumer: 2 KiB every 5 ms against a 32 KiB receive buffer.
  b.receiver.startAutoConsume({ chunkBytes: 2 * KiB, intervalMs: 5 });
  const received = collect(b.receiver);

  const suspends: number[] = [];
  a.sender.on('suspend', (e) => suspends.push(e.bytesInFlight));

  const payload = makePayload(256 * KiB); // fast injection of a "large file"
  a.send(payload);
  await a.sender.waitForDrain();
  drainReceiver(b.receiver);

  assert.equal(violations.length, 0, violations.join('\n'));
  assert.ok(suspends.length >= 1, 'sender must suspend at least once');
  assert.ok(
    suspends.every((n) => n <= 32 * KiB),
    `in-flight bytes never exceed the advertised window, got ${suspends}`,
  );
  assert.ok(a.sender.stats.suspensions >= 1);
  assert.equal(sha256(Buffer.concat(received)), sha256(payload));
});

test('window-shrink notification throttles sender until window grows again', async (t) => {
  const a = new Endpoint({ name: 'A', capacity: 256 * KiB, probeIntervalMs: 10 });
  const b = new Endpoint({ name: 'B', capacity: 64 * KiB, probeIntervalMs: 10 });
  const link = new Link(1);
  link.connect(a, b);
  t.after(() => {
    a.close();
    b.close();
  });
  const violations = makeWindowInvariantMonitor(link, a, b);

  const received = collect(b.receiver);
  const suspendWindows: number[] = [];
  a.sender.on('suspend', (e) => suspendWindows.push(e.window));

  const payload = makePayload(192 * KiB);
  a.send(payload);

  // Let the pipe fill, then actively push a window-shrink notification.
  await sleep(30);
  b.receiver.setCapacity(8 * KiB);
  assert.equal(b.receiver.window <= 8 * KiB, true);

  // Slow trickle of consumption under the shrunk window.
  b.receiver.startAutoConsume({ chunkBytes: 1 * KiB, intervalMs: 5 });
  await sleep(120);
  assert.ok(
    a.sender.stats.suspensions >= 1,
    'sender suspended under the shrunk window',
  );
  assert.ok(a.sender.pendingBytes > 0, 'transfer still backlogged while throttled');

  // Grow the window back: the transfer must complete.
  b.receiver.setCapacity(64 * KiB);
  b.receiver.startAutoConsume({ chunkBytes: 8 * KiB, intervalMs: 2 });
  await a.sender.waitForDrain();
  drainReceiver(b.receiver);

  assert.equal(violations.length, 0, violations.join('\n'));
  assert.equal(sha256(Buffer.concat(received)), sha256(payload));
});

test('zero window enters probe mode; sender stays parked until window reopens', async (t) => {
  const a = new Endpoint({ name: 'A', capacity: 256 * KiB, probeIntervalMs: 10 });
  const b = new Endpoint({ name: 'B', capacity: 16 * KiB, probeIntervalMs: 10 });
  const link = new Link(1);
  link.connect(a, b);
  t.after(() => {
    a.close();
    b.close();
  });
  const violations = makeWindowInvariantMonitor(link, a, b);

  // Track DATA frames sent while the latest beacon window is zero.
  let lastWindow = -1;
  let dataWhileZeroWindow = 0;
  link.addObserver((frame, from) => {
    if (from === b) lastWindow = frame.window;
    if (from === a && frame.type === FrameType.DATA && lastWindow === 0) {
      dataWhileZeroWindow++;
    }
  });

  const probes: number[] = [];
  a.sender.on('probe', () => probes.push(Date.now()));

  // Consumer is stopped: the 16 KiB buffer fills and the window hits zero.
  const received = collect(b.receiver);
  const payload = makePayload(64 * KiB);
  a.send(payload);

  await sleep(150); // several probe intervals while the window is zero
  assert.equal(b.receiver.window, 0, 'receive buffer is full, window is zero');
  assert.ok(probes.length >= 2, `probe mode active, got ${probes.length} probes`);
  assert.equal(dataWhileZeroWindow, 0, 'no DATA frames sent while window is zero');
  assert.ok(a.sender.isSuspended);
  assert.ok(b.receiver.stats.probesAnswered >= probes.length - 1);

  // Reopen the window: probing stops and the transfer completes.
  b.receiver.startAutoConsume({ chunkBytes: 4 * KiB, intervalMs: 2 });
  await a.sender.waitForDrain();
  drainReceiver(b.receiver);

  const probesAfterDrain = a.sender.stats.probesSent;
  await sleep(50);
  assert.equal(a.sender.stats.probesSent, probesAfterDrain, 'probing stopped');
  assert.equal(violations.length, 0, violations.join('\n'));
  assert.equal(sha256(Buffer.concat(received)), sha256(payload));
});

test('bidirectional simultaneous transfer with piggybacked beacons', async (t) => {
  const a = new Endpoint({ name: 'A', capacity: 32 * KiB, probeIntervalMs: 10 });
  const b = new Endpoint({ name: 'B', capacity: 48 * KiB, probeIntervalMs: 10 });
  const link = new Link(1);
  link.connect(a, b);
  t.after(() => {
    a.close();
    b.close();
  });
  const violationsAB = makeWindowInvariantMonitor(link, a, b);
  const violationsBA = makeWindowInvariantMonitor(link, b, a);

  a.receiver.startAutoConsume({ chunkBytes: 3 * KiB, intervalMs: 3 });
  b.receiver.startAutoConsume({ chunkBytes: 2 * KiB, intervalMs: 3 });
  const receivedByA = collect(a.receiver);
  const receivedByB = collect(b.receiver);

  const payloadAB = makePayload(128 * KiB, 0xaaaa);
  const payloadBA = makePayload(96 * KiB, 0xbbbb);
  a.send(payloadAB);
  b.send(payloadBA);

  await Promise.all([a.sender.waitForDrain(), b.sender.waitForDrain()]);
  drainReceiver(a.receiver);
  drainReceiver(b.receiver);

  assert.equal(violationsAB.length, 0, violationsAB.join('\n'));
  assert.equal(violationsBA.length, 0, violationsBA.join('\n'));
  assert.equal(sha256(Buffer.concat(receivedByB)), sha256(payloadAB));
  assert.equal(sha256(Buffer.concat(receivedByA)), sha256(payloadBA));
});
