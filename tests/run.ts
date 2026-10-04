import assert from 'node:assert/strict';
import { Document, SourceRegistry, REDACTION_CHAR } from '../src/provenance.ts';
import type { ReaderId, SourceId, VisibilityReport } from '../src/provenance.ts';
import { ReferenceDocument } from '../src/reference.ts';

let failures = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (err) {
    failures++;
    console.error(`FAIL - ${name}`);
    console.error(err);
  }
}

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function makeRegistry(): SourceRegistry {
  const registry = new SourceRegistry();
  registry.register('public', ['alice', 'bob', 'carol']);
  registry.register('team', ['alice', 'bob']);
  registry.register('alice-only', ['alice']);
  registry.register('bob-only', ['bob']);
  registry.register('sealed', []);
  return registry;
}

test('duplicate references: copies stay redacted and cite the same source', () => {
  const registry = makeRegistry();
  const doc = Document.fromFragment(registry, 'team', 'secret');
  doc.copyRange(0, 6, 6);
  doc.copyRange(0, 6, 12);
  assert.equal(doc.toString(), 'secretsecretsecret');

  const report = doc.visibleFor('carol');
  assert.equal(report.text, REDACTION_CHAR.repeat(18));
  assert.deepEqual(report.redactions, [{ start: 0, end: 18, reasons: ['team'] }]);

  doc.deleteRange(0, 6);
  const after = doc.visibleFor('carol');
  assert.equal(after.text, REDACTION_CHAR.repeat(12));
  assert.deepEqual(after.redactions, [{ start: 0, end: 12, reasons: ['team'] }]);

  assert.equal(doc.visibleFor('bob').text, 'secretsecret');
});

test('deletion removes redaction intervals without stale reasons', () => {
  const registry = makeRegistry();
  const doc = new Document(registry);
  doc.appendFragment('public', 'aa');
  doc.appendFragment('alice-only', 'bb');
  doc.appendFragment('public', 'cc');

  let report = doc.visibleFor('bob');
  assert.equal(report.text, `aa${REDACTION_CHAR}${REDACTION_CHAR}cc`);
  assert.deepEqual(report.redactions, [{ start: 2, end: 4, reasons: ['alice-only'] }]);

  doc.deleteRange(2, 4);
  report = doc.visibleFor('bob');
  assert.equal(report.text, 'aacc');
  assert.deepEqual(report.redactions, []);
});

test('cross-permission-boundary replacement keeps neighbor provenance', () => {
  const registry = makeRegistry();
  const doc = new Document(registry);
  doc.appendFragment('public', 'aa');
  doc.appendFragment('alice-only', 'BB');
  doc.appendFragment('public', 'cc');

  doc.replaceRange(1, 4, 'bob-only', 'XYZ');
  assert.equal(doc.toString(), 'aXYZcc');

  const alice = doc.visibleFor('alice');
  assert.equal(alice.text, `a${REDACTION_CHAR}${REDACTION_CHAR}${REDACTION_CHAR}cc`);
  assert.deepEqual(alice.redactions, [{ start: 1, end: 4, reasons: ['bob-only'] }]);

  const bob = doc.visibleFor('bob');
  assert.equal(bob.text, 'aXYZcc');
  assert.deepEqual(bob.redactions, []);

  const carol = doc.visibleFor('carol');
  assert.equal(carol.text, `a${REDACTION_CHAR}${REDACTION_CHAR}${REDACTION_CHAR}cc`);
  assert.deepEqual(carol.redactions, [{ start: 1, end: 4, reasons: ['bob-only'] }]);
});

test('move preserves provenance across permission boundaries', () => {
  const registry = makeRegistry();
  const doc = new Document(registry);
  doc.appendFragment('public', 'public.');
  doc.appendFragment('team', 'TEAM');
  doc.appendFragment('public', '.tail');

  doc.moveRange(7, 11, 0);
  assert.equal(doc.toString(), 'TEAMpublic..tail');

  const report = doc.visibleFor('carol');
  assert.equal(report.text, `${REDACTION_CHAR.repeat(4)}public..tail`);
  assert.deepEqual(report.redactions, [{ start: 0, end: 4, reasons: ['team'] }]);
});

test('formatting never widens visibility', () => {
  const registry = makeRegistry();
  const doc = new Document(registry);
  doc.appendFragment('public', 'say hello ');
  doc.appendFragment('alice-only', ' secret  data ');
  doc.appendFragment('public', 'again');

  doc.mapChars((ch) => ch.toUpperCase());
  let report = doc.visibleFor('bob');
  assert.equal(report.text, `SAY HELLO ${REDACTION_CHAR.repeat(14)}AGAIN`);
  assert.deepEqual(report.redactions, [{ start: 10, end: 24, reasons: ['alice-only'] }]);

  doc.collapseWhitespace();
  assert.equal(doc.toString(), 'SAY HELLO SECRET DATA AGAIN');
  report = doc.visibleFor('bob');
  assert.equal(report.text, `SAY HELLO${REDACTION_CHAR.repeat(13)}AGAIN`);
  assert.deepEqual(report.redactions, [{ start: 9, end: 22, reasons: ['alice-only'] }]);
  assert.equal(doc.visibleFor('alice').text, 'SAY HELLO SECRET DATA AGAIN');
});

test('overlay merges sources so readers become the intersection', () => {
  const registry = makeRegistry();
  const base = Document.fromFragment(registry, 'team', 'abcd');
  const extra = Document.fromFragment(registry, 'alice-only', 'wxyz');
  base.overlay(extra);
  assert.equal(base.toString(), 'abcd');

  const bob = base.visibleFor('bob');
  assert.equal(bob.text, REDACTION_CHAR.repeat(4));
  assert.deepEqual(bob.redactions, [
    { start: 0, end: 4, reasons: ['alice-only'] },
  ]);
  assert.equal(base.visibleFor('alice').text, 'abcd');
  assert.equal(base.visibleFor('carol').redactions[0].reasons.join(','), 'alice-only,team');
});

test('literals are unrestricted but overlay with a fragment restricts them', () => {
  const registry = makeRegistry();
  const doc = new Document(registry);
  doc.insertLiteral(0, 'free');
  assert.equal(doc.visibleFor('carol').text, 'free');
  assert.deepEqual(doc.visibleFor('carol').redactions, []);

  const locked = Document.fromFragment(registry, 'sealed', 'LOCK');
  doc.overlay(locked);
  const report = doc.visibleFor('alice');
  assert.equal(report.text, REDACTION_CHAR.repeat(4));
  assert.deepEqual(report.redactions, [{ start: 0, end: 4, reasons: ['sealed'] }]);
});

test('insertDocument concatenates fragments with provenance intact', () => {
  const registry = makeRegistry();
  const left = Document.fromFragment(registry, 'public', 'ab');
  const right = Document.fromFragment(registry, 'bob-only', 'cd');
  left.insertDocument(1, right);
  assert.equal(left.toString(), 'acdb');
  const report = left.visibleFor('alice');
  assert.equal(report.text, `a${REDACTION_CHAR}${REDACTION_CHAR}b`);
  assert.deepEqual(report.redactions, [{ start: 1, end: 3, reasons: ['bob-only'] }]);
});

const READERS: ReaderId[] = ['alice', 'bob', 'carol', 'dave'];
const ALPHABET = 'ab \n';

interface FuzzOp {
  kind: string;
  args: (number | string)[];
  overlayPieces?: [SourceId, string][];
}

function randomSource(rand: () => number): SourceId {
  return `src-${Math.floor(rand() * 5)}`;
}

function randomText(rand: () => number, maxLen: number): string {
  const len = 1 + Math.floor(rand() * maxLen);
  let out = '';
  for (let i = 0; i < len; i++) {
    out += ALPHABET[Math.floor(rand() * ALPHABET.length)];
  }
  return out;
}

function randomRange(rand: () => number, length: number): [number, number] {
  const start = Math.floor(rand() * (length + 1));
  const end = start + Math.floor(rand() * (length - start + 1));
  return [start, end];
}

function makeOp(rand: () => number, length: number): FuzzOp {
  const kinds = length === 0
    ? ['insertFragment', 'insertLiteral']
    : [
        'insertFragment',
        'insertLiteral',
        'copyRange',
        'moveRange',
        'deleteRange',
        'replaceRange',
        'overlay',
        'mapChars',
        'collapseWhitespace',
      ];
  const kind = kinds[Math.floor(rand() * kinds.length)];
  switch (kind) {
    case 'insertFragment':
      return {
        kind,
        args: [Math.floor(rand() * (length + 1)), randomSource(rand), randomText(rand, 5)],
      };
    case 'insertLiteral':
      return { kind, args: [Math.floor(rand() * (length + 1)), randomText(rand, 4)] };
    case 'copyRange': {
      const [start, end] = randomRange(rand, length);
      return { kind, args: [start, end, Math.floor(rand() * (length + 1))] };
    }
    case 'moveRange': {
      const [start, end] = randomRange(rand, length);
      return { kind, args: [start, end, Math.floor(rand() * (length + 2))] };
    }
    case 'deleteRange':
      return { kind, args: randomRange(rand, length) };
    case 'replaceRange': {
      const [start, end] = randomRange(rand, length);
      return { kind, args: [start, end, randomSource(rand), randomText(rand, 4)] };
    }
    case 'overlay': {
      const pieces: [SourceId, string][] = [];
      const count = 1 + Math.floor(rand() * 3);
      for (let i = 0; i < count; i++) {
        pieces.push([randomSource(rand), randomText(rand, 4)]);
      }
      return { kind, args: [], overlayPieces: pieces };
    }
    case 'mapChars':
      return { kind, args: [Math.floor(rand() * 3)] };
    default:
      return { kind: 'collapseWhitespace', args: [] };
  }
}

function applyOp(
  registry: SourceRegistry,
  doc: Document | ReferenceDocument,
  op: FuzzOp,
): void {
  switch (op.kind) {
    case 'insertFragment':
      doc.insertFragment(op.args[0] as number, op.args[1] as SourceId, op.args[2] as string);
      break;
    case 'insertLiteral':
      doc.insertLiteral(op.args[0] as number, op.args[1] as string);
      break;
    case 'copyRange':
      doc.copyRange(op.args[0] as number, op.args[1] as number, op.args[2] as number);
      break;
    case 'moveRange':
      doc.moveRange(op.args[0] as number, op.args[1] as number, op.args[2] as number);
      break;
    case 'deleteRange':
      doc.deleteRange(op.args[0] as number, op.args[1] as number);
      break;
    case 'replaceRange':
      doc.replaceRange(
        op.args[0] as number,
        op.args[1] as number,
        op.args[2] as SourceId,
        op.args[3] as string,
      );
      break;
    case 'overlay':
      if (doc instanceof Document) {
        const other = new Document(registry);
        for (const [source, text] of op.overlayPieces ?? []) {
          other.appendFragment(source, text);
        }
        doc.overlay(other);
      } else {
        const other = new ReferenceDocument(registry);
        for (const [source, text] of op.overlayPieces ?? []) {
          other.appendFragment(source, text);
        }
        doc.overlay(other);
      }
      break;
    case 'mapChars': {
      const variant = op.args[0] as number;
      if (variant === 0) doc.mapChars((ch) => ch.toUpperCase());
      else if (variant === 1) doc.mapChars((ch) => (ch === 'a' ? 'aa' : ch));
      else doc.mapChars((ch) => (ch === 'b' ? '' : ch));
      break;
    }
    default:
      doc.collapseWhitespace();
  }
}

test('differential fuzz: span document matches per-character reference', () => {
  for (let seed = 1; seed <= 40; seed++) {
    const rand = mulberry32(seed);
    const registry = new SourceRegistry();
    for (let i = 0; i < 5; i++) {
      const readers = READERS.filter(() => rand() < 0.7);
      registry.register(`src-${i}`, readers);
    }
    const doc = new Document(registry);
    const ref = new ReferenceDocument(registry);
    const ops = 1 + Math.floor(rand() * 120);
    for (let step = 0; step < ops; step++) {
      const op = makeOp(rand, doc.length);
      applyOp(registry, doc, op);
      applyOp(registry, ref, op);
      assert.equal(
        doc.toString(),
        ref.toString(),
        `text mismatch at seed ${seed} step ${step} (${op.kind})`,
      );
      for (const reader of READERS) {
        assert.deepEqual(
          doc.visibleFor(reader) as VisibilityReport,
          ref.visibleFor(reader) as VisibilityReport,
          `visibility mismatch for ${reader} at seed ${seed} step ${step} (${op.kind})`,
        );
      }
    }
  }
});

if (failures > 0) {
  console.error(`${failures} test(s) failed`);
  process.exit(1);
}
console.log('all tests passed');
