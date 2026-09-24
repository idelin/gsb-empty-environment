import { TextDoc } from '../src/index.ts';

function show(label: string, doc: TextDoc): void {
  console.log(`  ${label}: ${JSON.stringify(doc.getText())} (utf16 length=${doc.length})`);
}

console.log('--- 1. Local editing with UTF-16 offsets (emoji + CRLF) ---');
const a = new TextDoc('A');
a.insertAt(0, 'hi \u{1F600}\r\nbye');
show('A after insert', a);
a.deleteAt(5, 2); // remove the CRLF (2 UTF-16 units, offsets 5..6)
show('A after deleting CRLF', a);
try {
  a.insertAt(4, 'x'); // offset 4 sits inside the emoji surrogate pair
} catch (err) {
  console.log(`  splitting a surrogate pair is rejected: ${(err as Error).message}`);
}

console.log('\n--- 2. Out-of-order delivery: insert buffered until its parent arrives ---');
const seed = a.insertAt(a.length, '!');
const tailOp = seed[0]; // parented on the last visible element
const b = new TextDoc('B');
b.apply(tailOp); // parent unknown to B
show('B (parent missing, NOT attached to head)', b);
// Deliver the whole history; the buffered op drains automatically.
const history = a.snapshot();
const c = TextDoc.from(history, 'C');
b.merge(c);
show('B after history arrives', b);

console.log('\n--- 3. Delete before insert: char stays invisible; duplicates are no-ops ---');
const x = new TextDoc('X');
const ins = x.insertAt(0, 'z');
const del = x.deleteAt(0, 1);
const y = new TextDoc('Y');
y.apply(del[0]); // delete first
y.apply(ins[0]); // insert later
y.apply(ins[0]); // duplicate
y.apply(del[0]); // duplicate
show('Y (delete-before-insert)', y);

console.log('\n--- 4. Concurrent edits converge via merge (order-independent) ---');
const p = new TextDoc('P');
const q = new TextDoc('Q');
p.insertAt(0, 'apple');
q.insertAt(0, 'banana');
const pq = TextDoc.merged(p, q);
const qp = TextDoc.merged(q, p);
show('P.merge(Q)', pq);
show('Q.merge(P)', qp);
console.log(`  commutative: ${pq.getText() === qp.getText()}, associative+idempotent: see tests`);

console.log('\n--- 5. Watermark GC: tombstones collected only after ALL replicas ack ---');
const g1 = new TextDoc('G1');
const g2 = new TextDoc('G2');
for (const op of g1.insertAt(0, 'temp')) g2.apply(op);
for (const op of g1.deleteAt(0, 4)) g2.apply(op);
show('G1 after deleting everything', g1);
console.log(`  gc with only G1 acking: collected ${g1.gc(new Map([['G1', g1.watermark()], ['G2', new Map()]])).length} element(s)`);
const ackAll = new Map([
  ['G1', g1.watermark()],
  ['G2', g2.watermark()],
]);
console.log(`  gc after G1+G2 both ack:  collected ${g1.gc(ackAll).length} element(s)`);
