import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RichTextDocument } from '../src/index.ts';
import type { BoldMark, EntityMark, LinkMark } from '../src/index.ts';

const bolds = (doc: RichTextDocument) => doc.marks.filter((m) => m.type === 'bold') as BoldMark[];
const links = (doc: RichTextDocument) => doc.marks.filter((m) => m.type === 'link') as LinkMark[];
const entities = (doc: RichTextDocument) =>
  doc.marks.filter((m) => m.type === 'entity') as EntityMark[];

// ------------------------------------------------------------- bold rules

test('adjacent bold ranges with same attributes merge', () => {
  const doc = new RichTextDocument('abcdefghi');
  doc.addBold(0, 5);
  doc.addBold(5, 9);
  assert.deepEqual(bolds(doc), [{ type: 'bold', start: 0, end: 9 }]);
});

test('overlapping bold ranges merge', () => {
  const doc = new RichTextDocument('abcdefghi');
  doc.addBold(0, 6);
  doc.addBold(3, 9);
  assert.deepEqual(bolds(doc), [{ type: 'bold', start: 0, end: 9 }]);
});

test('bold ranges separated by a gap stay distinct', () => {
  const doc = new RichTextDocument('abcdefghi');
  doc.addBold(0, 2);
  doc.addBold(4, 6);
  assert.deepEqual(bolds(doc), [
    { type: 'bold', start: 0, end: 2 },
    { type: 'bold', start: 4, end: 6 },
  ]);
});

test('insert inside bold expands it; at boundaries it does not', () => {
  const inside = new RichTextDocument('hello');
  inside.addBold(0, 5);
  inside.insert(2, 'X');
  assert.equal(inside.text, 'heXllo');
  assert.deepEqual(bolds(inside), [{ type: 'bold', start: 0, end: 6 }]);

  const atStart = new RichTextDocument('hello');
  atStart.addBold(0, 5);
  atStart.insert(0, 'X');
  assert.deepEqual(bolds(atStart), [{ type: 'bold', start: 1, end: 6 }]);

  const atEnd = new RichTextDocument('hello');
  atEnd.addBold(0, 5);
  atEnd.insert(5, 'X');
  assert.deepEqual(bolds(atEnd), [{ type: 'bold', start: 0, end: 5 }]);
});

test('deleting all marked text drops the empty mark', () => {
  const doc = new RichTextDocument('abcdef');
  doc.addBold(0, 3);
  doc.remove(0, 3);
  assert.equal(doc.text, 'def');
  assert.deepEqual(doc.marks, []);
});

// ------------------------------------------------------------- link rules

test('overlapping link overrides and keeps old link remnants', () => {
  const doc = new RichTextDocument('hello world');
  doc.addLink(0, 10, '/a');
  doc.addLink(4, 8, '/b');
  assert.deepEqual(links(doc), [
    { type: 'link', start: 0, end: 4, href: '/a' },
    { type: 'link', start: 4, end: 8, href: '/b' },
    { type: 'link', start: 8, end: 10, href: '/a' },
  ]);
});

test('new link fully covering an old link replaces it', () => {
  const doc = new RichTextDocument('hello world');
  doc.addLink(0, 10, '/a');
  doc.addLink(0, 10, '/b');
  assert.deepEqual(links(doc), [{ type: 'link', start: 0, end: 10, href: '/b' }]);
});

test('replace inside a link stretches it over the new text', () => {
  const doc = new RichTextDocument('click here now');
  doc.addLink(0, 10, '/h');
  doc.replace(6, 9, 'XYZW');
  assert.equal(doc.text, 'click XYZWe now');
  assert.deepEqual(links(doc), [{ type: 'link', start: 0, end: 11, href: '/h' }]);
});

// ----------------------------------------------------------- entity rules

test('overlapping entities are rejected', () => {
  const doc = new RichTextDocument('abcdefghi');
  doc.addEntity(0, 5, 'mention');
  assert.throws(() => doc.addEntity(3, 8, 'mention'), /overlaps existing entity/);
  doc.addEntity(5, 9, 'hashtag'); // adjacent is fine
  assert.equal(entities(doc).length, 2);
});

test('edit inside an indivisible entity invalidates it with a reason', () => {
  const doc = new RichTextDocument('hello @alice world');
  doc.addEntity(6, 12, 'mention', { id: 'u1' });
  const result = doc.insert(9, 'x');
  assert.equal(doc.text, 'hello @alxice world');
  assert.deepEqual(entities(doc), []);
  assert.equal(result.invalidations.length, 1);
  assert.equal(result.invalidations[0].reason, 'entity-interior-edited');
  assert.deepEqual(result.invalidations[0].entity, {
    type: 'entity',
    start: 6,
    end: 12,
    kind: 'mention',
    data: { id: 'u1' },
  });
  assert.match(result.invalidations[0].detail, /inside entity \[6, 12\)/);
});

test('delete partially overlapping an entity invalidates the whole entity', () => {
  const doc = new RichTextDocument('hello @alice world');
  doc.addEntity(6, 12, 'mention');
  const result = doc.remove(10, 14);
  assert.equal(doc.text, 'hello @aliorld');
  assert.deepEqual(entities(doc), []);
  assert.equal(result.invalidations[0].reason, 'entity-interior-edited');
});

test('delete fully covering an entity reports entity-deleted', () => {
  const doc = new RichTextDocument('hello @alice world');
  doc.addEntity(6, 12, 'mention');
  const result = doc.remove(6, 12);
  assert.equal(doc.text, 'hello  world');
  assert.deepEqual(entities(doc), []);
  assert.equal(result.invalidations[0].reason, 'entity-deleted');
});

test('edits at entity boundaries leave the entity intact', () => {
  const before = new RichTextDocument('hello @alice world');
  before.addEntity(6, 12, 'mention');
  const r1 = before.insert(6, '>');
  assert.equal(r1.invalidations.length, 0);
  assert.deepEqual(entities(before), [
    { type: 'entity', start: 7, end: 13, kind: 'mention' },
  ]);

  const after = new RichTextDocument('hello @alice world');
  after.addEntity(6, 12, 'mention');
  const r2 = after.insert(12, '<');
  assert.equal(r2.invalidations.length, 0);
  assert.deepEqual(entities(after), [
    { type: 'entity', start: 6, end: 12, kind: 'mention' },
  ]);
});

// --------------------------------------------------------- surrogate pairs

test('insert offset inside a surrogate pair snaps before the pair', () => {
  const doc = new RichTextDocument('a\u{1F600}b'); // a | 😀(2 units) | b
  assert.equal(doc.length, 4);
  assert.equal(doc.snapPoint(2), 1);
  assert.equal(doc.snapRangeEnd(2), 3);
  doc.insert(2, 'x');
  assert.equal(doc.text, 'ax\u{1F600}b');
});

test('remove range cutting a surrogate pair expands to the whole pair', () => {
  const doc = new RichTextDocument('a\u{1F600}b');
  doc.remove(1, 2); // would cut the pair; expands to [1, 3)
  assert.equal(doc.text, 'ab');
});

test('mark range cutting a surrogate pair snaps outward', () => {
  const doc = new RichTextDocument('a\u{1F600}b');
  doc.addBold(2, 4); // start snaps 2 -> 1
  assert.deepEqual(bolds(doc), [{ type: 'bold', start: 1, end: 4 }]);
});

test('edits around emoji keep offsets in UTF-16 units', () => {
  const doc = new RichTextDocument('\u{1F600}xy');
  doc.addBold(0, 2); // exactly the emoji
  doc.insert(2, 'z');
  assert.equal(doc.text, '\u{1F600}zxy');
  assert.deepEqual(bolds(doc), [{ type: 'bold', start: 0, end: 2 }]);
});

// ------------------------------------------------------------ LF and CRLF

test('CRLF counts as two UTF-16 units; marks and edits work across lines', () => {
  const doc = new RichTextDocument('ab\r\ncd\nef');
  doc.addBold(1, 5); // "b\r\nc"
  assert.equal(doc.text.slice(1, 5), 'b\r\nc');

  doc.insert(3, 'X'); // between \r and \n, inside the bold
  assert.equal(doc.text, 'ab\rX\ncd\nef');
  assert.deepEqual(bolds(doc), [{ type: 'bold', start: 1, end: 6 }]);

  doc.remove(2, 4); // remove "\rX"
  assert.equal(doc.text, 'ab\ncd\nef');
  assert.deepEqual(bolds(doc), [{ type: 'bold', start: 1, end: 4 }]);
});

test('mixed LF/CRLF text survives a serialize round trip byte-identically', () => {
  const doc = new RichTextDocument('a\r\nb\nc\r\n');
  doc.addLink(0, 5, '/x');
  const restored = RichTextDocument.deserialize(doc.serialize());
  assert.equal(restored.text, 'a\r\nb\nc\r\n');
  assert.equal(restored.toCanonicalJSON(), doc.toCanonicalJSON());
});

// ---------------------------------------------------------- serialization

test('canonical serialization is independent of insertion order', () => {
  const a = new RichTextDocument('abcdefghijkl');
  a.addBold(0, 5);
  a.addLink(2, 6, '/x');
  a.addBold(6, 9);
  a.addEntity(10, 12, 'tag');

  const b = new RichTextDocument('abcdefghijkl');
  b.addEntity(10, 12, 'tag');
  b.addBold(6, 9);
  b.addLink(2, 6, '/x');
  b.addBold(0, 5);

  assert.equal(a.toCanonicalJSON(), b.toCanonicalJSON());
});

test('canonical JSON sorts entity data keys recursively', () => {
  const doc = new RichTextDocument('ab');
  doc.addEntity(0, 2, 'tag', { b: 1, a: { d: 4, c: 3 } });
  const json = doc.toCanonicalJSON();
  assert.ok(json.indexOf('"a"') < json.indexOf('"b"'));
  assert.ok(json.indexOf('"c"') < json.indexOf('"d"'));
});

test('serialize/deserialize round trip is stable', () => {
  const doc = new RichTextDocument('hello @alice \u{1F600}\r\nworld');
  doc.addBold(0, 5);
  doc.addEntity(6, 12, 'mention', { id: 'u1' });
  doc.addLink(16, 21, '/w');
  const once = RichTextDocument.deserialize(doc.serialize());
  const twice = RichTextDocument.deserialize(once.serialize());
  assert.equal(once.toCanonicalJSON(), doc.toCanonicalJSON());
  assert.equal(twice.toCanonicalJSON(), doc.toCanonicalJSON());
});

test('marks getter returns copies, not live references', () => {
  const doc = new RichTextDocument('hello');
  doc.addBold(0, 5);
  const snapshot = doc.marks;
  (snapshot[0] as { start: number }).start = 99;
  assert.deepEqual(bolds(doc), [{ type: 'bold', start: 0, end: 5 }]);
});
