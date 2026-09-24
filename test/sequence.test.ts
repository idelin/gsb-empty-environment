import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { TextSequence, type Op } from "../src/sequence.ts";

function deliver(target: TextSequence, ops: Op[]): void {
  for (const op of ops) target.apply(op);
}

describe("basic editing with UTF-16 offsets", () => {
  it("inserts and reads text, length in UTF-16 code units", () => {
    const doc = new TextSequence("A");
    doc.insertAt(0, "hello");
    assert.equal(doc.toString(), "hello");
    assert.equal(doc.length, 5);
    doc.insertAt(5, " world");
    doc.insertAt(0, ">> ");
    assert.equal(doc.toString(), ">> hello world");
  });

  it("deletes a UTF-16 range", () => {
    const doc = new TextSequence("A");
    doc.insertAt(0, "hello world");
    const ops = doc.deleteAt(5, 6);
    assert.equal(doc.toString(), "hello");
    assert.equal(ops.length, 6);
  });

  it("never splits a surrogate pair on insert", () => {
    const doc = new TextSequence("A");
    doc.insertAt(0, "a\u{1F600}b"); // a + emoji + b -> offsets 0, 1..2, 3
    assert.equal(doc.length, 4);
    doc.insertAt(2, "X"); // mid-pair: clamped down to offset 1
    assert.equal(doc.toString(), "aX\u{1F600}b");
    doc.insertAt(4, "Y"); // boundary right after the pair
    assert.equal(doc.toString(), "aX\u{1F600}Yb");
  });

  it("never splits a surrogate pair on delete", () => {
    const doc = new TextSequence("A");
    doc.insertAt(0, "a\u{1D11E}b"); // a + musical symbol + b
    doc.deleteAt(1, 1); // range covers only the high surrogate
    assert.equal(doc.toString(), "ab"); // whole pair removed

    const doc2 = new TextSequence("A");
    doc2.insertAt(0, "a\u{1D11E}b");
    doc2.deleteAt(2, 1); // starts mid-pair: clamped to pair start
    assert.equal(doc2.toString(), "ab");
  });

  it("covers LF and CRLF line endings", () => {
    const doc = new TextSequence("A");
    doc.insertAt(0, "l1\nl2\r\nl3");
    assert.equal(doc.length, "l1\nl2\r\nl3".length); // 9 UTF-16 units
    doc.deleteAt(5, 2); // remove the CRLF
    assert.equal(doc.toString(), "l1\nl2l3");
    doc.insertAt(3, "\r\n"); // insert a CRLF pair after the LF
    assert.equal(doc.toString(), "l1\n\r\nl2l3");
    doc.deleteAt(0, doc.length);
    assert.equal(doc.toString(), "");
    assert.equal(doc.length, 0);
  });
});

describe("causal delivery", () => {
  it("buffers inserts whose dependency is missing instead of attaching to head", () => {
    const a = new TextSequence("A");
    const ops = a.insertAt(0, "ab"); // a=(A,1) left=null, b=(A,2) left=(A,1)
    const b = new TextSequence("B");
    b.apply(ops[1]); // only "b" arrives; its left dependency (A,1) is unknown
    assert.equal(b.toString(), ""); // not visible, and NOT attached to head
    assert.equal(b.bufferedCount, 1);
    b.apply(ops[0]); // dependency arrives
    assert.equal(b.toString(), "ab");
    assert.equal(b.bufferedCount, 0);
  });

  it("converges under fully shuffled delivery", () => {
    const a = new TextSequence("A");
    const ops = a.insertAt(0, "shuffle me \u{1F30D}\r\n!");
    const b = new TextSequence("B");
    const shuffled = ops.slice().reverse();
    deliver(b, shuffled);
    assert.equal(b.toString(), a.toString());
    assert.equal(b.length, a.length);
  });

  it("keeps a character invisible when its delete arrived before the insert", () => {
    const a = new TextSequence("A");
    const [insertX] = a.insertAt(0, "X");
    const [deleteX] = a.deleteAt(0, 1);
    const b = new TextSequence("B");
    b.apply(deleteX); // delete first
    b.apply(insertX); // insert second
    assert.equal(b.toString(), "");
    assert.equal(b.tombstoneCount, 1);
  });

  it("ignores duplicate deliveries", () => {
    const a = new TextSequence("A");
    const inserts = a.insertAt(0, "dup");
    const deletes = a.deleteAt(0, 1);
    const b = new TextSequence("B");
    for (let round = 0; round < 3; round++) {
      deliver(b, inserts);
      deliver(b, deletes);
    }
    assert.equal(b.toString(), "up");
    assert.equal(b.getOps().length, inserts.length + deletes.length);
    assert.equal(b.tombstoneCount, 1);
  });

  it("treats repeated deletes as idempotent tombstones", () => {
    const a = new TextSequence("A");
    a.insertAt(0, "zz");
    const deletes = a.deleteAt(0, 2);
    const b = new TextSequence("B");
    deliver(b, a.getOps());
    deliver(b, deletes); // same deletes again
    assert.equal(b.toString(), "");
    assert.equal(b.tombstoneCount, 2);
  });
});

describe("merge semantics", () => {
  function buildReplicas(): { a: TextSequence; b: TextSequence; c: TextSequence } {
    const a = new TextSequence("A");
    const b = new TextSequence("B");
    const c = new TextSequence("C");
    const seed = a.insertAt(0, "seed ");
    deliver(b, seed);
    deliver(c, seed);
    a.insertAt(5, "from-A\u{1F600}");
    b.insertAt(0, "B>");
    c.insertAt(5, "C ");
    a.deleteAt(0, 2); // delete "se"
    return { a, b, c };
  }

  it("is commutative", () => {
    const { a, b } = buildReplicas();
    const ab = new TextSequence("M1");
    ab.merge(a);
    ab.merge(b);
    const ba = new TextSequence("M2");
    ba.merge(b);
    ba.merge(a);
    assert.equal(ab.toString(), ba.toString());
    assert.equal(ab.toString(), "B>ed from-A\u{1F600}");
  });

  it("is associative", () => {
    const { a, b, c } = buildReplicas();
    const left = new TextSequence("M1");
    left.merge(a);
    left.merge(b);
    left.merge(c);
    const right = new TextSequence("M2");
    right.merge(a);
    const bc = new TextSequence("M3");
    bc.merge(b);
    bc.merge(c);
    right.merge(bc);
    assert.equal(left.toString(), right.toString());
  });

  it("is idempotent", () => {
    const { a, b } = buildReplicas();
    const m = new TextSequence("M");
    m.merge(a);
    m.merge(b);
    const text1 = m.toString();
    const ops1 = m.getOps().length;
    m.merge(a);
    m.merge(b);
    m.merge(m);
    assert.equal(m.toString(), text1);
    assert.equal(m.getOps().length, ops1);
  });

  it("converges for concurrent inserts at the same position", () => {
    const a = new TextSequence("A");
    const b = new TextSequence("B");
    const opsA = a.insertAt(0, "aaa");
    const opsB = b.insertAt(0, "bb");
    deliver(a, opsB);
    deliver(b, opsA);
    assert.equal(a.toString(), b.toString());
    assert.equal(a.toString().replace(/[ab]/g, "").length, 0);
    assert.equal(a.length, 5);
  });
});

describe("garbage collection", () => {
  function setup(): { a: TextSequence; b: TextSequence; c: TextSequence } {
    const a = new TextSequence("A");
    const b = new TextSequence("B");
    const c = new TextSequence("C");
    a.insertAt(0, "xy");
    a.deleteAt(0, 1); // tombstone for "x"
    for (const r of [b, c]) deliver(r, a.getOps());
    a.merge(b); // replica discovery: a now knows b and c exist
    a.merge(c);
    return { a, b, c };
  }

  function fullAcks(replicas: TextSequence[]): Record<string, Record<string, number>> {
    const acks: Record<string, Record<string, number>> = {};
    for (const r of replicas) acks[r.replicaId] = r.getStateVector();
    return acks;
  }

  it("refuses to collect while any replica has not confirmed the watermark", () => {
    const { a, b } = setup();
    const partial = fullAcks([a, b]); // c has not acked
    assert.equal(a.collectGarbage(partial), 0);
    assert.equal(a.tombstoneCount, 1);
  });

  it("collects tombstones once every replica confirmed, preserving text", () => {
    const { a, b, c } = setup();
    assert.equal(a.tombstoneCount, 1);
    const collected = a.collectGarbage(fullAcks([a, b, c]));
    assert.equal(collected, 1);
    assert.equal(a.tombstoneCount, 0);
    assert.equal(a.toString(), "y");
  });

  it("ignores late duplicates of collected elements", () => {
    const { a, b, c } = setup();
    const opsBefore = a.getOps();
    a.collectGarbage(fullAcks([a, b, c]));
    deliver(a, opsBefore); // stale redelivery after GC
    assert.equal(a.toString(), "y");
    assert.equal(a.tombstoneCount, 0);
  });

  it("keeps later inserts anchored after a collected tombstone", () => {
    const a = new TextSequence("A");
    const b = new TextSequence("B");
    a.insertAt(0, "mn");
    a.deleteAt(0, 1); // tombstone "m"; "n" is anchored after it
    deliver(b, a.getOps());
    a.merge(b); // discover replica b
    a.collectGarbage(fullAcks([a, b]));
    assert.equal(a.toString(), "n");
    a.insertAt(1, "!"); // must land after "n", not get lost with the tombstone
    assert.equal(a.toString(), "n!");
  });
});
