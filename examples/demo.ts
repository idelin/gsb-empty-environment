import { InMemoryNetwork } from '../src/transport.ts';
import { messageBytes } from '../src/protocol.ts';

const DOC_SIZE = 10_000;
const ALPHABET = 'abcdefghijklmnopqrstuvwxyz0123456789';

function fmt(bytes: number): string {
  return bytes >= 1024 ? `${(bytes / 1024).toFixed(1)} KiB` : `${bytes} B`;
}

// --- setup: node A authors a 10k-character document -------------------------
const net = new InMemoryNetwork();
const a = net.createNode('A');
const b = net.createNode('B');
for (let i = 0; i < DOC_SIZE; i++) a.append(ALPHABET[i % ALPHABET.length]);

console.log(`document size: ${DOC_SIZE} chars (CRDT nodes)`);
console.log(
  `full state transfer: ${fmt(messageBytes({
    type: 'delta',
    from: 'A',
    base: {},
    ops: a.doc.serializeState(),
    version: a.doc.version(),
  }))}`,
);

// --- initial sync: B joins and receives the full state ----------------------
net.resetStats();
net.connect('A', 'B');
console.log(`\n[initial sync] wire traffic: ${net.stats.messages} msgs, ${fmt(net.stats.bytes)}`);
console.log(`[initial sync] B in sync: ${b.doc.fingerprint() === a.doc.fingerprint()}`);
console.log(`[initial sync] A's ack watermark for B: ${JSON.stringify(a.ackWatermark('B'))}`);

// --- incremental sync: modify exactly 1 character ---------------------------
net.resetStats();
a.insertAt(DOC_SIZE >> 1, 'X');

const delta = net.log.find((e) => e.msg.type === 'delta' && e.to === 'B')!;
console.log(`\n[1-char edit] delta packet: ${fmt(delta.bytes)} (${net.stats.messages} msgs, ${fmt(net.stats.bytes)} total incl. ack)`);
console.log(`[1-char edit] delta ops: ${delta.msg.type === 'delta' ? delta.msg.ops.length : '-'}`);
console.log(`[1-char edit] delta payload: ${JSON.stringify(delta.msg)}`);
console.log(`[1-char edit] B in sync: ${b.doc.fingerprint() === a.doc.fingerprint()}`);
console.log(`[1-char edit] texts equal: ${b.doc.text() === a.doc.text()}`);

// --- no changes => no traffic -----------------------------------------------
net.resetStats();
a.broadcast();
console.log(`\n[no changes] extra traffic after ack: ${net.stats.messages} msgs`);

// --- equivalence with a fresh full-state transfer ---------------------------
const c = net.createNode('C');
c.receive({
  type: 'delta',
  from: 'A',
  base: {},
  ops: a.doc.serializeState(),
  version: a.doc.version(),
});
console.log(`[equivalence] delta-synced B == full-state C: ${b.doc.fingerprint() === c.doc.fingerprint()}`);
