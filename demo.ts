/**
 * Runnable sample: two sites edit a shared document concurrently.
 * Run with: node demo.ts
 */
import { applyOp, applyOps, del, insert, transform } from './src/ot.ts';
import { Session } from './src/session.ts';

function show(label: string, value: unknown): void {
  console.log(label.padEnd(34), JSON.stringify(value));
}

// --- 1. Same-position inserts converge in any delivery order ---------------
const base = 'ab';
const opA = insert(1, 'X'); // site 1
const opB = insert(1, 'Y'); // site 2

const orderAB = applyOps(applyOp(base, opA), transform(opB, 2, opA, 1));
const orderBA = applyOps(applyOp(base, opB), transform(opA, 1, opB, 2));
console.log('--- same-position inserts ---');
show('base', base);
show('site1 inserts "X" @1, site2 "Y" @1', '');
show('deliver A-then-B', orderAB);
show('deliver B-then-A', orderBA);
show('converged', orderAB === orderBA);

// --- 2. Overlapping deletes do not double-delete ---------------------------
const doc2 = 'abcdef';
const delA = del(1, 3); // "bcd"
const delB = del(3, 3); // "def"
const delAB = applyOps(applyOp(doc2, delA), transform(delB, 2, delA, 1));
const delBA = applyOps(applyOp(doc2, delB), transform(delA, 1, delB, 2));
console.log('\n--- overlapping deletes ---');
show('base', doc2);
show('site1 deletes [1,4), site2 [3,6)', '');
show('deliver A-then-B', delAB);
show('deliver B-then-A', delBA);
show('converged', delAB === delBA);

// --- 3. Versioned session with baseline validation -------------------------
console.log('\n--- session (CRLF document) ---');
const session = new Session('one\r\ntwo');
show('initial doc', session.doc);
show('version', session.version);

session.receive({ op: insert(3, '1.5\r\n'), baseVersion: 0, siteId: 1 });
show('after site1 insert @3', session.doc);

// site2 was offline: its delete is still based on version 0.
const forwarded = session.receive({ op: del(4, 3), baseVersion: 0, siteId: 2 });
show('site2 delete [4,7) based on v0', 'accepted');
show('transformed forward to', forwarded);
show('final doc', session.doc);
show('version', session.version);

try {
  session.receive({ op: insert(0, '!'), baseVersion: 99, siteId: 3 });
} catch (err) {
  show('baseline 99 rejected', (err as Error).message);
}

// --- 4. Surrogate-pair protection ------------------------------------------
console.log('\n--- surrogate-pair protection (UTF-16 offsets) ---');
const emojiDoc = 'a\uD83D\uDE00b'; // "a😀b"
show('doc', emojiDoc);
show('insert "🎉" at UTF-16 offset 1', applyOp(emojiDoc, insert(1, '\uD83C\uDF89')));
try {
  applyOp(emojiDoc, insert(2, 'X'));
} catch (err) {
  show('insert at offset 2 rejected', (err as Error).message);
}
