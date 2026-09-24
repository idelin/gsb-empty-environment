import { test } from "node:test";
import assert from "node:assert/strict";
import { RichTextDocument } from "../src/richtext.ts";

// ----------------------------------------------------------- UTF-16 offsets

test("offsets are UTF-16 code units: an astral char occupies 2", () => {
  const doc = new RichTextDocument("a😀b"); // a=0, 😀=[1,3), b=3
  assert.equal(doc.length, 4);
  doc.setBold(3, 4); // bold only "b"
  assert.deepEqual(doc.getBold(), [{ start: 3, end: 4 }]);
});

test("insert/delete/attributes may never split a surrogate pair", () => {
  const doc = new RichTextDocument("a😀b");
  assert.throws(() => doc.insert(2, "x"), /surrogate pair/);
  assert.throws(() => doc.delete(1, 2), /surrogate pair/);
  assert.throws(() => doc.delete(2, 3), /surrogate pair/);
  assert.throws(() => doc.setBold(0, 2), /surrogate pair/);
  assert.throws(() => doc.setLink(2, 4, "h"), /surrogate pair/);
  assert.throws(() => doc.setEntity(1, 2, "mention"), /surrogate pair/);
  // boundaries around the pair are fine
  doc.insert(3, "!");
  assert.equal(doc.getText(), "a😀!b");
  doc.delete(0, 3); // removes "a😀" as a whole
  assert.equal(doc.getText(), "!b");
});

test("astral text shifts attributes by code units, not code points", () => {
  const doc = new RichTextDocument("😀xy");
  doc.setBold(2, 4); // "xy"
  doc.insert(0, "😀"); // 2 code units
  assert.deepEqual(doc.getBold(), [{ start: 4, end: 6 }]);
  assert.equal(doc.getText().slice(4, 6), "xy");
});

// -------------------------------------------------------------- LF / CRLF

test("LF line endings: newline counts as 1 code unit", () => {
  const doc = new RichTextDocument("a\nb");
  doc.setBold(2, 3); // "b"
  assert.deepEqual(doc.getBold(), [{ start: 2, end: 3 }]);
  doc.delete(1, 2); // remove "\n"
  assert.equal(doc.getText(), "ab");
  assert.deepEqual(doc.getBold(), [{ start: 1, end: 2 }]);
});

test("CRLF line endings: \\r\\n counts as 2 code units", () => {
  const doc = new RichTextDocument("a\r\nb");
  assert.equal(doc.length, 4);
  doc.setBold(3, 4); // "b"
  assert.deepEqual(doc.getBold(), [{ start: 3, end: 4 }]);
  doc.delete(1, 3); // remove "\r\n"
  assert.equal(doc.getText(), "ab");
  assert.deepEqual(doc.getBold(), [{ start: 1, end: 2 }]);
});

test("mixed LF and CRLF survive edits across the boundary", () => {
  const doc = new RichTextDocument("x\ny\r\nz");
  doc.setLink(0, 6, "https://e");
  doc.delete(1, 5); // delete "\ny\r\n" -> "xz"
  assert.equal(doc.getText(), "xz");
  assert.deepEqual(doc.getLinks(), [{ start: 0, end: 2, href: "https://e" }]);
});

// ------------------------------------------------------------------- bold

test("adjacent bold ranges with the same attribute merge", () => {
  const doc = new RichTextDocument("abcdef");
  doc.setBold(0, 2);
  doc.setBold(2, 4); // adjacent -> merge
  doc.setBold(3, 6); // overlapping -> merge
  assert.deepEqual(doc.getBold(), [{ start: 0, end: 6 }]);
});

test("removing bold in the middle splits the range", () => {
  const doc = new RichTextDocument("abcdef");
  doc.setBold(0, 6);
  doc.setBold(2, 4, false);
  assert.deepEqual(doc.getBold(), [
    { start: 0, end: 2 },
    { start: 4, end: 6 },
  ]);
});

test("insert inside bold extends it; insert at boundary does not", () => {
  const doc = new RichTextDocument("abcdef");
  doc.setBold(1, 5);
  doc.insert(3, "XX"); // inside
  assert.deepEqual(doc.getBold(), [{ start: 1, end: 7 }]);
  doc.insert(1, "Y"); // at start boundary: stays outside
  assert.deepEqual(doc.getBold(), [{ start: 2, end: 8 }]);
  doc.insert(8, "Z"); // at end boundary: stays outside
  assert.deepEqual(doc.getBold(), [{ start: 2, end: 8 }]);
});

// ------------------------------------------------------------------- links

test("overlapping link: new link covers its range, old keeps residual", () => {
  const doc = new RichTextDocument("0123456789");
  doc.setLink(0, 6, "old");
  doc.setLink(4, 10, "new");
  assert.deepEqual(doc.getLinks(), [
    { start: 0, end: 4, href: "old" }, // residual segment
    { start: 4, end: 10, href: "new" },
  ]);
});

test("link inside an old link splits it into two residual segments", () => {
  const doc = new RichTextDocument("0123456789");
  doc.setLink(0, 10, "old");
  doc.setLink(3, 6, "new");
  assert.deepEqual(doc.getLinks(), [
    { start: 0, end: 3, href: "old" },
    { start: 3, end: 6, href: "new" },
    { start: 6, end: 10, href: "old" },
  ]);
});

test("fully covered old link disappears; removeLink clears a subrange", () => {
  const doc = new RichTextDocument("0123456789");
  doc.setLink(2, 4, "old");
  doc.setLink(0, 6, "new");
  assert.deepEqual(doc.getLinks(), [{ start: 0, end: 6, href: "new" }]);
  doc.removeLink(1, 5);
  assert.deepEqual(doc.getLinks(), [
    { start: 0, end: 1, href: "new" },
    { start: 5, end: 6, href: "new" },
  ]);
});

// ---------------------------------------------------------------- entities

test("entity invalidated as a whole when inserting inside, with reason", () => {
  const doc = new RichTextDocument("hello @alice world");
  doc.setEntity(6, 12, "mention", "u1"); // "@alice" at [6,12)
  const events = doc.insert(9, "X");
  assert.equal(events.length, 1);
  assert.equal(events[0].type, "entity-invalidated");
  assert.equal(events[0].reason, "insert-inside-entity");
  assert.deepEqual(events[0].entity, { start: 6, end: 12, kind: "mention", id: "u1" });
  assert.deepEqual(doc.getEntities(), []);
  assert.equal(doc.getText(), "hello @alXice world");
});

test("insert at entity boundary leaves the entity intact", () => {
  const doc = new RichTextDocument("hello @alice");
  doc.setEntity(6, 12, "mention");
  assert.equal(doc.insert(6, "> ").length, 0); // before: shifts right
  assert.deepEqual(doc.getEntities(), [{ start: 8, end: 14, kind: "mention" }]);
  assert.equal(doc.insert(14, "!").length, 0); // after: unchanged
  assert.deepEqual(doc.getEntities(), [{ start: 8, end: 14, kind: "mention" }]);
});

test("delete intersecting an entity invalidates it wholly, with reason", () => {
  const doc = new RichTextDocument("hello @alice world");
  doc.setEntity(6, 12, "mention", "u1");
  const events = doc.delete(10, 15); // cuts into the entity's tail
  assert.equal(events.length, 1);
  assert.equal(events[0].reason, "delete-intersects-entity");
  assert.deepEqual(doc.getEntities(), []); // whole entity gone
  assert.equal(doc.getText(), "hello @alirld"); // surviving text stays
});

test("delete fully covering an entity removes it with its text", () => {
  const doc = new RichTextDocument("hello @alice world");
  doc.setEntity(6, 12, "mention");
  const events = doc.delete(6, 13); // entity + following space
  assert.equal(events.length, 0);
  assert.deepEqual(doc.getEntities(), []);
  assert.equal(doc.getText(), "hello world");
});

test("entities never overlap: a new entity replaces intersecting ones wholly", () => {
  const doc = new RichTextDocument("aaabbbccc");
  doc.setEntity(0, 4, "token", "t1"); // "aaab"
  const events = doc.setEntity(2, 6, "token", "t2"); // "abbc" overlaps t1
  assert.equal(events.length, 1);
  assert.equal(events[0].reason, "replaced-by-new-entity");
  assert.deepEqual(events[0].entity, { start: 0, end: 4, kind: "token", id: "t1" });
  assert.deepEqual(doc.getEntities(), [{ start: 2, end: 6, kind: "token", id: "t2" }]);
});

// ----------------------------------------------------- canonical serialize

test("serialization is canonical and insertion-order independent", () => {
  // Same logical state built via two different operation orders.
  const a = new RichTextDocument("hello world");
  a.setBold(0, 5);
  a.setBold(5, 11);
  a.setLink(0, 5, "https://a");
  a.setLink(6, 11, "https://b");

  const b = new RichTextDocument("hello world");
  b.setLink(6, 11, "https://b");
  b.setBold(3, 11);
  b.setLink(0, 5, "https://a");
  b.setBold(0, 3);

  assert.equal(a.serialize(), b.serialize());
  assert.equal(
    a.serialize(),
    '{"text":"hello world",' +
      '"bold":[{"start":0,"end":11}],' +
      '"links":[{"start":0,"end":5,"href":"https://a"},' +
      '{"start":6,"end":11,"href":"https://b"}],' +
      '"entities":[]}',
  );
});

test("serialization is stable across edits and re-serialization", () => {
  const doc = new RichTextDocument("😀ab\r\ncd");
  doc.setBold(2, 4);
  doc.setEntity(6, 8, "hashtag", "h1");
  const first = doc.serialize();
  assert.equal(doc.serialize(), first);
  assert.deepEqual(JSON.parse(first), {
    text: "😀ab\r\ncd",
    bold: [{ start: 2, end: 4 }],
    links: [],
    entities: [{ start: 6, end: 8, kind: "hashtag", id: "h1" }],
  });
});

test("intervals serialize sorted regardless of application order", () => {
  const doc = new RichTextDocument("0123456789");
  doc.setLink(6, 8, "b");
  doc.setLink(0, 2, "a");
  doc.setEntity(4, 5, "token");
  doc.setEntity(8, 9, "token");
  const parsed = JSON.parse(doc.serialize());
  assert.deepEqual(
    parsed.links.map((l: { href: string }) => l.href),
    ["a", "b"],
  );
  assert.deepEqual(
    parsed.entities.map((e: { start: number }) => e.start),
    [4, 8],
  );
});

// ------------------------------------------------------------- validation

test("invalid ranges are rejected", () => {
  const doc = new RichTextDocument("abc");
  assert.throws(() => doc.delete(2, 2), /invalid range/);
  assert.throws(() => doc.delete(-1, 2), /invalid range/);
  assert.throws(() => doc.setBold(1, 9), /invalid range/);
  assert.throws(() => doc.insert(4, "x"), /invalid offset/);
  assert.throws(() => doc.insert(1.5, "x"), /invalid offset/);
});
