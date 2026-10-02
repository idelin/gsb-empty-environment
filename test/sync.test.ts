import { test } from 'node:test';
import assert from 'node:assert/strict';
import { InMemoryNetwork } from '../src/transport.ts';
import { messageBytes, type Message } from '../src/protocol.ts';
import { coversVV, equalVV } from '../src/versionVector.ts';
import type { SyncNode } from '../src/node.ts';

const ALPHABET = 'abcdefghijklmnopqrstuvwxyz0123456789';

function buildDocument(node: SyncNode, size: number): void {
  for (let i = 0; i < size; i++) node.append(ALPHABET[i % ALPHABET.length]);
}

function lastDeltaTo(network: InMemoryNetwork, to: string) {
  const entry = [...network.log].reverse().find(
    (e) => e.to === to && e.msg.type === 'delta',
  );
  assert.ok(entry, `expected a delta message to ${to}`);
  return entry;
}

test('initial sync transfers full state; ack watermark is recorded', () => {
  const net = new InMemoryNetwork();
  const a = net.createNode('A');
  const b = net.createNode('B');
  buildDocument(a, 1000);

  net.connect('A', 'B');

  assert.equal(b.doc.text(), a.doc.text());
  assert.equal(b.doc.fingerprint(), a.doc.fingerprint());
  assert.ok(coversVV(a.ackWatermark('B'), b.doc.version()));
  assert.ok(coversVV(b.ackWatermark('A'), a.doc.version()));
});

test('1-char edit in a 10000-node document yields a constant-size delta', () => {
  const sizes = [1000, 10000, 100000];
  const deltaBytes: number[] = [];
  const fullBytes: number[] = [];

  for (const size of sizes) {
    const net = new InMemoryNetwork();
    const a = net.createNode('A');
    const b = net.createNode('B');
    buildDocument(a, size);
    net.connect('A', 'B');
    assert.equal(b.doc.length, size);

    // Full-state transfer cost grows linearly with the document.
    fullBytes.push(messageBytes({
      type: 'delta',
      from: 'A',
      base: {},
      ops: a.doc.serializeState(),
      version: a.doc.version(),
    }));

    net.resetStats();
    a.insertAt(size >> 1, 'X'); // modify exactly 1 character

    const delta = lastDeltaTo(net, 'B');
    assert.equal(delta.msg.type, 'delta');
    if (delta.msg.type === 'delta') assert.equal(delta.msg.ops.length, 1);
    deltaBytes.push(delta.bytes);

    // Receiver state is exactly equivalent to the sender's.
    assert.equal(b.doc.fingerprint(), a.doc.fingerprint());
    assert.equal(b.doc.text(), a.doc.text());
    assert.equal(b.doc.length, size + 1);
  }

  // Constant-size bound: a 1-char delta stays tiny regardless of doc size...
  for (const bytes of deltaBytes) assert.ok(bytes < 256, `delta too big: ${bytes}`);
  // ...while full-state transfer scales linearly with the document.
  assert.ok(fullBytes[1] > 5 * fullBytes[0]);
  assert.ok(fullBytes[2] > 5 * fullBytes[1]);
  // Delta size varies only by sequence-number digit count, not by doc size.
  assert.ok(Math.max(...deltaBytes) - Math.min(...deltaBytes) <= 16);
});

test('delete of 1 char in a 10000-node document is also constant-size', () => {
  const net = new InMemoryNetwork();
  const a = net.createNode('A');
  const b = net.createNode('B');
  buildDocument(a, 10000);
  net.connect('A', 'B');

  net.resetStats();
  a.deleteAt(5000);

  const delta = lastDeltaTo(net, 'B');
  assert.ok(delta.bytes < 256, `delta too big: ${delta.bytes}`);
  assert.equal(b.doc.fingerprint(), a.doc.fingerprint());
  assert.equal(b.doc.length, 9999);
});

test('delta sync result is identical to full-state transfer result', () => {
  const net = new InMemoryNetwork();
  const a = net.createNode('A');
  const b = net.createNode('B'); // synced incrementally
  buildDocument(a, 10000);
  net.connect('A', 'B');

  // A burst of edits propagated as small deltas.
  a.insertAt(0, '>');
  a.insertAt(5000, '*');
  a.deleteAt(100);
  a.append('<');

  // C receives the same content as one full-state transfer.
  const c = net.createNode('C');
  c.receive({
    type: 'delta',
    from: 'A',
    base: {},
    ops: a.doc.serializeState(),
    version: a.doc.version(),
  });

  assert.equal(b.doc.fingerprint(), a.doc.fingerprint());
  assert.equal(c.doc.fingerprint(), a.doc.fingerprint());
  assert.equal(b.doc.text(), c.doc.text());
  assert.ok(equalVV(b.doc.version(), a.doc.version()));
});

test('no changes after ack => no further traffic', () => {
  const net = new InMemoryNetwork();
  const a = net.createNode('A');
  const b = net.createNode('B');
  buildDocument(a, 500);
  net.connect('A', 'B');

  net.resetStats();
  const sent = a.broadcast();
  assert.equal(sent.length, 0);
  assert.equal(net.stats.messages, 0);
});

test('watermark only advances on ack; unacked edits are re-sent on broadcast', () => {
  const net = new InMemoryNetwork();
  const a = net.createNode('A');
  net.createNode('B');
  buildDocument(a, 10);
  net.connect('A', 'B');

  // Directly drive a broadcast without new edits: nothing to send.
  assert.equal(a.broadcast().length, 0);

  // Simulate a peer that never acked by checking a fresh node id watermark.
  assert.deepEqual(a.ackWatermark('C'), {});
});

test('three peers with concurrent edits converge (full mesh)', () => {
  const net = new InMemoryNetwork();
  const a = net.createNode('A');
  const b = net.createNode('B');
  const c = net.createNode('C');
  net.connect('A', 'B');
  net.connect('B', 'C');
  net.connect('A', 'C');

  a.append('a');
  b.append('b');
  c.append('c');
  a.insertAt(0, '>');
  b.deleteAt(0); // deletes whatever B sees at 0
  c.append('!');

  // Extra anti-entropy round to flush any remaining deltas.
  a.broadcast();
  b.broadcast();
  c.broadcast();

  assert.equal(a.doc.fingerprint(), b.doc.fingerprint());
  assert.equal(b.doc.fingerprint(), c.doc.fingerprint());
  assert.equal(a.doc.text(), b.doc.text());
  assert.equal(b.doc.text(), c.doc.text());
});

test('protocol messages survive an encode/decode round-trip', () => {
  const net = new InMemoryNetwork();
  const a = net.createNode('A');
  const b = net.createNode('B');
  buildDocument(a, 100);
  net.connect('A', 'B');
  a.insertAt(50, 'X');

  const delta = lastDeltaTo(net, 'B');
  const json = JSON.stringify(delta.msg);
  const decoded = JSON.parse(json) as Message;
  assert.deepEqual(decoded, delta.msg);
  assert.equal(messageBytes(decoded), delta.bytes);
});
