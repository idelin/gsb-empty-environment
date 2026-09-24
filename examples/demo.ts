import { TextSequence, type Op } from "../src/sequence.ts";

function show(label: string, doc: TextSequence): void {
  console.log(
    `${label}: ${JSON.stringify(doc.toString())} (utf16 length=${doc.length}, tombstones=${doc.tombstoneCount}, buffered=${doc.bufferedCount})`,
  );
}

// --- 1. Alice types offline; ops include an emoji (surrogate pair) and CRLF/LF.
const alice = new TextSequence("alice");
const insertOps = alice.insertAt(0, "Hello \u{1F30D}\r\nsecond line\n");
show("alice typed", alice);

// --- 2. Bob receives the ops shuffled, with duplicates: still converges.
const bob = new TextSequence("bob");
const shuffled = insertOps.slice().reverse();
for (const op of [...shuffled, ...shuffled]) bob.apply(op); // duplicated delivery
show("bob after shuffled+duplicated delivery", bob);

// --- 3. Delete arrives before its insert: the char stays invisible.
const carolOps = new TextSequence("carol");
const [insertX] = carolOps.insertAt(0, "X");
const [deleteX] = carolOps.deleteAt(0, 1);
const dave = new TextSequence("dave");
dave.apply(deleteX); // delete first...
dave.apply(insertX); // ...insert later
show("dave (delete-before-insert)", dave);

// --- 4. An insert with a missing dependency is buffered, never put at head.
const f1 = new TextSequence("F");
const chain = f1.insertAt(0, "ab");
const f2 = new TextSequence("G");
f2.apply(chain[1]); // "b" depends on "a" which is missing
show("replica G with missing dependency", f2);
f2.apply(chain[0]);
show("replica G after dependency arrived", f2);

// --- 5. Concurrent edits merge commutatively / associatively / idempotently.
const opsA: Op[] = alice.getOps();
const bobEdits = bob.insertAt(0, "[bob] ");
const aliceEdits = alice.insertAt(alice.length, " [alice]");
bob.merge(alice); // state-based merge both ways
alice.merge(bob);
show("alice after merge", alice);
show("bob after merge", bob);
console.log("merge converged:", alice.toString() === bob.toString());
console.log("merge idempotent:", (alice.merge(bob), alice.toString() === bob.toString()));
void opsA;
void bobEdits;
void aliceEdits;

// --- 6. Tombstone GC only after every replica confirms the watermark.
alice.deleteAt(0, 6); // delete "[bob] "
bob.merge(alice);
show("alice before GC", alice);
const acksWithoutBob = { alice: alice.getStateVector() };
console.log("GC with only alice's ack ->", alice.collectGarbage(acksWithoutBob), "collected");
const fullAcks = {
  alice: alice.getStateVector(),
  bob: bob.getStateVector(),
};
console.log("GC with all replicas' acks ->", alice.collectGarbage(fullAcks), "collected");
show("alice after GC", alice);
