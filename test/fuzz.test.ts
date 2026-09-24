import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LayoutEngine, isMidSurrogatePair } from '../src/index.ts';

/** Deterministic PRNG so failures are reproducible. */
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

// Narrow, wide, zero-width, tab, surrogate pair, ZWJ sequence, LF, CRLF.
const ALPHABET = [
  'a', 'b', ' ', '1', 'x',
  '\t',
  '世', '界',
  'e', '́',
  '😀', '🧑‍💻',
  '\n', '\r\n',
];

function randomText(rng: () => number, maxTokens: number): string {
  const n = 1 + Math.floor(rng() * maxTokens);
  let out = '';
  for (let i = 0; i < n; i++) {
    out += ALPHABET[Math.floor(rng() * ALPHABET.length)];
  }
  return out;
}

/** All UTF-16 offsets that do not split a surrogate pair. */
function validOffsets(text: string): number[] {
  const offsets: number[] = [];
  for (let i = 0; i <= text.length; i++) {
    if (!isMidSurrogatePair(text, i)) {
      offsets.push(i);
    }
  }
  return offsets;
}

function assertLayoutInvariants(engine: LayoutEngine, text: string, wrapColumn = 8): void {
  assert.equal(engine.getText(), text);
  // Incremental layout must equal a from-scratch full layout.
  const fresh = new LayoutEngine({ text, tabWidth: 4, wrapColumn });
  assert.deepEqual(engine.allRows(), fresh.allRows());
  // Every visual position maps to a valid, non-surrogate-splitting offset.
  for (const row of engine.allRows()) {
    for (let column = 0; column <= row.width + 1; column++) {
      const { offset } = engine.visualToLogical({ line: row.line, row: row.row, column });
      assert.ok(offset >= 0 && offset <= text.length, `offset ${offset} in range`);
      assert.ok(!isMidSurrogatePair(text, offset), `offset ${offset} not mid-pair`);
    }
  }
}

test('random edits: incremental layout always matches full re-layout', () => {
  const rng = mulberry32(0x5eed);
  for (let iter = 0; iter < 150; iter++) {
    let text = randomText(rng, 25);
    const engine = new LayoutEngine({ text, tabWidth: 4, wrapColumn: 8 });
    const edits = 1 + Math.floor(rng() * 15);
    for (let e = 0; e < edits; e++) {
      const offsets = validOffsets(text);
      if (rng() < 0.6 || text.length === 0) {
        const at = offsets[Math.floor(rng() * offsets.length)];
        const inserted = randomText(rng, 5);
        engine.insert(at, inserted);
        text = text.slice(0, at) + inserted + text.slice(at);
      } else {
        const at = offsets[Math.floor(rng() * offsets.length)];
        const later = offsets.filter((o) => o > at && o <= at + 10);
        if (later.length === 0) {
          continue;
        }
        const end = later[Math.floor(rng() * later.length)];
        engine.delete(at, end - at);
        text = text.slice(0, at) + text.slice(end);
      }
      assertLayoutInvariants(engine, text);
    }
  }
});

test('random documents: conversions stay consistent across seeds', () => {
  for (let seed = 1; seed <= 20; seed++) {
    const rng = mulberry32(seed);
    const text = randomText(rng, 60);
    const engine = new LayoutEngine({ text, tabWidth: 4, wrapColumn: 6 });
    assertLayoutInvariants(engine, text, 6);
    // logical -> visual -> logical round trip at every valid offset,
    // using the returned affinity to pick the direction preference.
    for (const offset of validOffsets(text)) {
      const visual = engine.logicalToVisual(offset, 'forward');
      const back = engine.visualToLogical(visual, 'start');
      const visual2 = engine.logicalToVisual(back.offset, back.affinity);
      assert.deepEqual(visual2, visual);
    }
  }
});
