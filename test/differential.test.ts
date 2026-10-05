import test from 'node:test';
import {
  FragmentDocument,
  ReferenceDocument,
  SourceRegistry,
  verifyAgainstReference,
} from '../src/index.ts';

const READERS = ['alice', 'bob', 'carol', 'dave'];
const ALL_READERS = [...READERS, 'mallory'];
const SOURCES = ['s0', 's1', 's2', 's3', 's4', 's5'];
const FORMAT_CHARS = [' ', '\n', '-', '•'];
const MAP_FNS: Array<(ch: string) => string> = [
  (ch) => ch.toUpperCase(),
  (ch) => ch + ch,
  (ch) => ch + ' ',
  () => '',
  (ch) => ch,
];

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

function randInt(rng: () => number, n: number): number {
  return Math.floor(rng() * n);
}

function randomRange(rng: () => number, length: number): [number, number] {
  const a = randInt(rng, length + 1);
  const b = randInt(rng, length + 1);
  return [Math.min(a, b), Math.max(a, b)];
}

function randomText(rng: () => number, maxLen: number, alphabet: string): string {
  const n = 1 + randInt(rng, maxLen);
  let out = '';
  for (let i = 0; i < n; i++) out += alphabet[randInt(rng, alphabet.length)];
  return out;
}

function runSeed(seed: number): void {
  const rng = mulberry32(seed);
  const registry = new SourceRegistry();
  for (const id of SOURCES) {
    const readers = READERS.filter(() => rng() < 0.6);
    registry.addSource(id, readers.length > 0 ? readers : [READERS[randInt(rng, READERS.length)]]);
  }
  const doc = new FragmentDocument(registry);
  const ref = new ReferenceDocument(registry);
  let mergeCounter = 0;

  for (let step = 0; step < 300; step++) {
    const length = ref.length;
    const op = randInt(rng, 9);
    switch (op) {
      case 0: {
        const src = SOURCES[randInt(rng, SOURCES.length)];
        const text = randomText(rng, 4, 'abc');
        doc.append(src, text);
        ref.append(src, text);
        break;
      }
      case 1: {
        const src = SOURCES[randInt(rng, SOURCES.length)];
        const text = randomText(rng, 3, 'ab');
        const pos = randInt(rng, length + 1);
        doc.insert(pos, src, text);
        ref.insert(pos, src, text);
        break;
      }
      case 2: {
        const [start, end] = randomRange(rng, length);
        const dest = randInt(rng, length + 1);
        doc.copyRange(start, end, dest);
        ref.copyRange(start, end, dest);
        break;
      }
      case 3: {
        const [start, end] = randomRange(rng, length);
        const dest = randInt(rng, length + 1);
        doc.moveRange(start, end, dest);
        ref.moveRange(start, end, dest);
        break;
      }
      case 4: {
        const [start, end] = randomRange(rng, length);
        doc.deleteRange(start, end);
        ref.deleteRange(start, end);
        break;
      }
      case 5: {
        const [start, end] = randomRange(rng, length);
        const text = randomText(rng, 3, 'xy');
        doc.replaceRange(start, end, text);
        ref.replaceRange(start, end, text);
        break;
      }
      case 6: {
        const [start, end] = randomRange(rng, length);
        const id = `m${mergeCounter++}`;
        doc.mergeRange(start, end, id);
        ref.mergeRange(start, end, id);
        break;
      }
      case 7: {
        const pos = randInt(rng, length + 1);
        const text = randomText(rng, 2, FORMAT_CHARS.join(''));
        doc.insertFormatting(pos, text);
        ref.insertFormatting(pos, text);
        break;
      }
      case 8: {
        const [start, end] = randomRange(rng, length);
        const fn = MAP_FNS[randInt(rng, MAP_FNS.length)];
        doc.mapRange(start, end, fn);
        ref.mapRange(start, end, fn);
        break;
      }
    }
    verifyAgainstReference(doc, ref, ALL_READERS);
  }
}

for (const seed of [1, 2, 3, 42, 2024]) {
  test(`差分验证 seed=${seed}：高效实现与逐字符参考实现一致`, () => {
    runSeed(seed);
  });
}
