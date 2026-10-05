import { AnnotationStore, Edit, Segment, Stickiness } from './annotations';
import { ReferenceModel } from './reference';

declare const process: { exit(code: number): never; argv: string[] };

let checks = 0;
let failures = 0;

function expectEq(actual: unknown, expected: unknown, label: string): void {
  checks++;
  const a = JSON.stringify(actual);
  const b = JSON.stringify(expected);
  if (a !== b) {
    failures++;
    console.error(`FAIL ${label}\n  actual:   ${a}\n  expected: ${b}`);
  }
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randInt(rand: () => number, lo: number, hi: number): number {
  return lo + Math.floor(rand() * (hi - lo + 1));
}

function randSticky(rand: () => number): Stickiness {
  return rand() < 0.5 ? 'left' : 'right';
}

function randText(rand: () => number, len: number): string {
  const alphabet = 'abc';
  let out = '';
  for (let i = 0; i < len; i++) out += alphabet[randInt(rand, 0, alphabet.length - 1)];
  return out;
}

function randEdit(rand: () => number, text: string): Edit {
  const len = text.length;
  const r = rand();
  if (r < 0.4 || len === 0) {
    return { kind: 'insert', pos: randInt(rand, 0, len), text: randText(rand, randInt(rand, 0, 3)) };
  }
  if (r < 0.7) {
    const start = randInt(rand, 0, len - 1);
    return { kind: 'delete', start, end: randInt(rand, start + 1, len) };
  }
  const start = randInt(rand, 0, len);
  const end = randInt(rand, start, len);
  return { kind: 'replace', start, end, text: randText(rand, randInt(rand, 0, 3)) };
}

function applyToString(text: string, edit: Edit): string {
  if (edit.kind === 'insert') return text.slice(0, edit.pos) + edit.text + text.slice(edit.pos);
  if (edit.kind === 'delete') return text.slice(0, edit.start) + text.slice(edit.end);
  return text.slice(0, edit.start) + edit.text + text.slice(edit.end);
}

function randBatch(rand: () => number, text: string, n: number): Edit[] {
  const edits: Edit[] = [];
  let shadow = text;
  for (let i = 0; i < n; i++) {
    const edit = randEdit(rand, shadow);
    edits.push(edit);
    shadow = applyToString(shadow, edit);
  }
  return edits;
}

function addRandomAnnotation(
  rand: () => number,
  store: AnnotationStore,
  ref: ReferenceModel,
  liveIds: number[],
): void {
  const len = store.length;
  const start = randInt(rand, 0, len);
  const end = rand() < 0.35 ? start : randInt(rand, start, len);
  const ss = randSticky(rand);
  const es = randSticky(rand);
  const id = store.addAnnotation(start, end, ss, es);
  const refId = ref.addAnnotation(start, end, ss, es);
  if (id !== refId) throw new Error(`id divergence: store=${id} ref=${refId}`);
  liveIds.push(id);
}

function refQueryIds(ref: ReferenceModel, s: number, e: number): number[] {
  const ids: number[] = [];
  for (const a of ref.debugAnnotations()) {
    if (!a.active) continue;
    if (a.kind === 'point') {
      const p = a.gapPosition;
      if (p === null) continue;
      if (s === e ? p === s : s <= p && p < e) ids.push(a.id);
    } else if (s === e) {
      if (a.covered.some((i) => i === s)) ids.push(a.id);
    } else if (a.covered.some((i) => s <= i && i < e)) {
      ids.push(a.id);
    }
  }
  return ids.sort((x, y) => x - y);
}

function checkState(
  store: AnnotationStore,
  ref: ReferenceModel,
  rand: () => number,
  label: string,
): void {
  expectEq(store.text, ref.text(), `${label}: text`);
  const len = store.text.length;

  const directChar: number[][] = Array.from({ length: len }, () => []);
  const directGap: number[][] = Array.from({ length: len + 1 }, () => []);
  for (const a of store.activeAnnotations()) {
    if (a.start === a.end) {
      directGap[a.start].push(a.id);
    } else {
      for (let i = a.start; i < a.end; i++) directChar[i].push(a.id);
    }
  }

  const segChar: number[][] = Array.from({ length: len }, () => []);
  const segGap: number[][] = Array.from({ length: len + 1 }, () => []);
  for (const seg of store.segments()) {
    if (seg.start === seg.end) {
      segGap[seg.start].push(...seg.ids);
    } else {
      for (let i = seg.start; i < seg.end; i++) segChar[i].push(...seg.ids);
    }
  }

  const refChar = ref.charCoverage();
  const refGap = ref.gapCoverage();
  expectEq(directChar, refChar, `${label}: per-char coverage (interval math)`);
  expectEq(segChar, refChar, `${label}: per-char coverage (segments output)`);
  expectEq(directGap, refGap, `${label}: per-gap zero-width (interval math)`);
  expectEq(segGap, refGap, `${label}: per-gap zero-width (segments output)`);
  expectEq(store.invalidIds(), ref.invalidIds(), `${label}: invalid tombstones`);

  const samples: Array<[number, number]> = [
    [0, 0],
    [0, len],
    [len, len],
  ];
  for (let k = 0; k < 4; k++) {
    const s = randInt(rand, 0, len);
    samples.push([s, randInt(rand, s, len)]);
  }
  for (const [s, e] of samples) {
    const got = store.query(s, e).map((a) => a.id);
    expectEq(got, refQueryIds(ref, s, e), `${label}: query(${s}, ${e})`);
  }
}

function expectAnn(
  store: AnnotationStore,
  id: number,
  start: number,
  end: number,
  active: boolean,
  label: string,
): void {
  const a = store.get(id);
  expectEq(a ? [a.start, a.end, a.active] : null, [start, end, active], label);
}

function scenarioBoundaryStickiness(): void {
  const store = new AnnotationStore('hello');
  const a1 = store.addAnnotation(1, 3, 'left', 'left');
  const a2 = store.addAnnotation(1, 3, 'left', 'right');
  const a3 = store.addAnnotation(1, 3, 'right', 'left');
  const a4 = store.addAnnotation(1, 3, 'right', 'right');

  store.applyEdit({ kind: 'insert', pos: 1, text: 'XX' });
  expectEq(store.text, 'hXXello', 'boundary: text after insert at start');
  expectAnn(store, a1, 1, 5, true, 'boundary: (left,left) expands at start');
  expectAnn(store, a2, 1, 5, true, 'boundary: (left,right) expands at start');
  expectAnn(store, a3, 3, 5, true, 'boundary: (right,left) stays at start');
  expectAnn(store, a4, 3, 5, true, 'boundary: (right,right) stays at start');

  store.undo();
  store.applyEdit({ kind: 'insert', pos: 3, text: 'Z' });
  expectEq(store.text, 'helZlo', 'boundary: text after insert at end');
  expectAnn(store, a1, 1, 3, true, 'boundary: (left,left) stays at end');
  expectAnn(store, a2, 1, 4, true, 'boundary: (left,right) expands at end');
  expectAnn(store, a3, 1, 3, true, 'boundary: (right,left) stays at end');
  expectAnn(store, a4, 1, 4, true, 'boundary: (right,right) expands at end');
}

function scenarioSamePointZeroWidth(): void {
  const store = new AnnotationStore('abc');
  const p1 = store.addAnnotation(1, 1, 'left');
  const p2 = store.addAnnotation(1, 1, 'right');
  const p3 = store.addAnnotation(0, 3);

  store.applyEdit({ kind: 'insert', pos: 1, text: 'XY' });
  expectEq(store.text, 'aXYbc', 'zero-width: text after insert');
  expectAnn(store, p1, 1, 1, true, 'zero-width: sticky-left stays before insert');
  expectAnn(store, p2, 3, 3, true, 'zero-width: sticky-right moves after insert');
  expectEq(store.segments(), [
    { start: 0, end: 5, ids: [p3] },
    { start: 1, end: 1, ids: [p1] },
    { start: 3, end: 3, ids: [p2] },
  ] as Segment[], 'zero-width: point segments split by stickiness');

  store.applyEdit({ kind: 'delete', start: 1, end: 3 });
  expectEq(store.text, 'abc', 'zero-width: text after deleting inserted text');
  expectAnn(store, p1, 1, 1, true, 'zero-width: boundary p===s survives');
  expectAnn(store, p2, 1, 1, true, 'zero-width: boundary p===e survives');
  expectEq(store.segments(), [
    { start: 0, end: 3, ids: [p3] },
    { start: 1, end: 1, ids: [p1, p2] },
  ] as Segment[], 'zero-width: same-point set ordered by id');

  store.applyEdit({ kind: 'delete', start: 0, end: 3 });
  expectEq(store.text, '', 'zero-width: text fully deleted');
  expectEq(store.invalidIds(), [p1, p2, p3], 'zero-width: all tombstoned');
  expectEq(store.segments(), [] as Segment[], 'zero-width: no active segments');

  store.undo();
  expectEq(store.text, 'abc', 'zero-width: undo restores text');
  expectAnn(store, p1, 1, 1, true, 'zero-width: undo relocates p1');
  expectAnn(store, p2, 1, 1, true, 'zero-width: undo relocates p2');
  expectAnn(store, p3, 0, 3, true, 'zero-width: undo relocates p3');

  store.undo();
  expectEq(store.text, 'aXYbc', 'zero-width: second undo restores insert');
  expectAnn(store, p2, 3, 3, true, 'zero-width: p2 back after insert point');
}

function scenarioBatchReplace(): void {
  const store = new AnnotationStore('aaa bbb ccc');
  const a = store.addAnnotation(0, 3);
  const b = store.addAnnotation(4, 7);
  const c = store.addAnnotation(8, 11);
  const d = store.addAnnotation(0, 11);

  store.applyEdits([
    { kind: 'replace', start: 0, end: 3, text: 'x' },
    { kind: 'replace', start: 2, end: 5, text: 'yy' },
    { kind: 'replace', start: 5, end: 8, text: 'zzz' },
  ]);
  expectEq(store.text, 'x yy zzz', 'batch: sequential replace text');
  expectEq(store.invalidIds(), [a, b, c], 'batch: fully replaced annotations tombstoned');
  expectAnn(store, d, 1, 5, true, 'batch: outer annotation remapped');
  expectEq(store.segments(), [{ start: 1, end: 5, ids: [d] }] as Segment[], 'batch: segments');

  store.undo();
  expectEq(store.text, 'aaa bbb ccc', 'batch: undo restores whole batch');
  expectEq(store.invalidIds(), [], 'batch: undo revives tombstones');
  expectAnn(store, a, 0, 3, true, 'batch: a relocated');
  expectAnn(store, b, 4, 7, true, 'batch: b relocated');
  expectAnn(store, c, 8, 11, true, 'batch: c relocated');
  expectAnn(store, d, 0, 11, true, 'batch: d relocated');
}

function scenarioLayeredOverlap(): void {
  const store = new AnnotationStore('0123456789');
  const x = store.addAnnotation(0, 4);
  const y = store.addAnnotation(2, 8);
  const z = store.addAnnotation(6, 10);
  expectEq(store.segments(), [
    { start: 0, end: 2, ids: [x] },
    { start: 2, end: 4, ids: [x, y] },
    { start: 4, end: 6, ids: [y] },
    { start: 6, end: 8, ids: [y, z] },
    { start: 8, end: 10, ids: [z] },
  ] as Segment[], 'layered: overlapping sets ordered by id');

  store.applyEdit({ kind: 'delete', start: 3, end: 7 });
  expectEq(store.text, '012789', 'layered: text after delete');
  expectAnn(store, x, 0, 3, true, 'layered: x shrunk');
  expectAnn(store, y, 2, 4, true, 'layered: y shrunk');
  expectAnn(store, z, 3, 6, true, 'layered: z shifted');
  expectEq(store.segments(), [
    { start: 0, end: 2, ids: [x] },
    { start: 2, end: 3, ids: [x, y] },
    { start: 3, end: 4, ids: [y, z] },
    { start: 4, end: 6, ids: [z] },
  ] as Segment[], 'layered: segments after delete');
}

function scenarioRandomized(iterations: number): void {
  for (let iter = 0; iter < iterations; iter++) {
    const rand = mulberry32(1000 + iter);
    const text = randText(rand, randInt(rand, 0, 12));
    const store = new AnnotationStore(text);
    const ref = new ReferenceModel(text);
    const liveIds: number[] = [];

    const initial = randInt(rand, 0, 6);
    for (let k = 0; k < initial; k++) addRandomAnnotation(rand, store, ref, liveIds);
    checkState(store, ref, rand, `iter ${iter} init`);

    const ops = randInt(rand, 1, 25);
    for (let op = 0; op < ops; op++) {
      const label = `iter ${iter} op ${op}`;
      const r = rand();
      if (r < 0.15) {
        addRandomAnnotation(rand, store, ref, liveIds);
      } else if (r < 0.2 && store.canUndo) {
        expectEq(store.undo(), ref.undo(), `${label}: undo pair`);
      } else if (r < 0.25 && liveIds.length > 0) {
        const id = liveIds[randInt(rand, 0, liveIds.length - 1)];
        expectEq(store.removeAnnotation(id), ref.removeAnnotation(id), `${label}: remove ${id}`);
      } else if (r < 0.35) {
        const edits = randBatch(rand, store.text, randInt(rand, 1, 3));
        store.applyEdits(edits);
        ref.applyEdits(edits);
      } else {
        const edit = randEdit(rand, store.text);
        store.applyEdit(edit);
        ref.applyEdit(edit);
      }
      checkState(store, ref, rand, label);
    }
  }
}

scenarioBoundaryStickiness();
scenarioSamePointZeroWidth();
scenarioBatchReplace();
scenarioLayeredOverlap();
const iterations = Number(process.argv[2]) || 300;
scenarioRandomized(iterations);

console.log(`${checks} checks, ${failures} failures`);
if (failures > 0) process.exit(1);
console.log('ALL PASS');
