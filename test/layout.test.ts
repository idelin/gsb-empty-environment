import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import {
  LayoutEngine,
  computeFullLayout,
  rowWidth,
} from '../src/index.ts';
import { PieceTable, isHighSurrogate, isLowSurrogate } from '../src/index.ts';
import type {
  GraphemeMetric,
  LayoutSnapshot,
} from '../src/index.ts';

const flattenFragments = (snapshot: LayoutSnapshot) =>
  snapshot.lines.flatMap((line) =>
    line.rows.flatMap((row) => row.fragments.map((f) => ({
      kind: f.kind, start: f.start, length: f.length, col: f.col, width: f.width,
    }))),
  );

const snapshotSignature = (snapshot: LayoutSnapshot) => ({
  height: snapshot.height,
  lines: snapshot.lines.map((line) => ({
    start: line.start,
    contentLength: line.contentLength,
    terminatorLength: line.terminatorLength,
    height: line.height,
    rows: line.rows.map((row) => ({
      softBreakAfter: row.softBreakAfter,
      fragments: row.fragments.map((f) => ({
        kind: f.kind,
        start: f.start,
        length: f.length,
        col: f.col,
        width: f.width,
      })),
    })),
  })),
});

/* ---------------- tab expansion ---------------- */

describe('tab expansion to tab stops', () => {
  test('a + tab reaches column 4 with tab width 4', () => {
    const snapshot = new LayoutEngine('a\tb').snapshot();
    assert.equal(snapshot.positionAt(2).column, 4);
    const tabs = flattenFragments(snapshot).filter((f) => f.kind === 'tab');
    assert.deepEqual(tabs, [{ kind: 'tab', start: 1, length: 1, col: 1, width: 3 }]);
  });

  test('tab in column 0 has full width', () => {
    const snapshot = new LayoutEngine('\tx').snapshot();
    assert.equal(snapshot.positionAt(1).column, 4);
    assert.equal(rowWidth(snapshot.lines[0].rows[0]), 5);
  });

  test('tab width rounds up to the next stop', () => {
    const snapshot = new LayoutEngine('ab\tc').snapshot();
    assert.equal(snapshot.positionAt(3).column, 4);
  });

  test('custom tab width', () => {
    const snapshot = new LayoutEngine('a\tb', { tabWidth: 8 }).snapshot();
    assert.equal(snapshot.positionAt(2).column, 8);
  });
});

/* ---------------- wide / zero-width clusters ---------------- */

describe('character widths', () => {
  test('wide characters occupy two columns', () => {
    const snapshot = new LayoutEngine('a你b').snapshot();
    assert.equal(snapshot.positionAt(2).column, 3);
    const fragments = flattenFragments(snapshot);
    assert.deepEqual(fragments.map((f) => f.width), [1, 2, 1]);
  });

  test('combining mark does not own a column', () => {
    const snapshot = new LayoutEngine('e\u0301x').snapshot();
    const fragments = flattenFragments(snapshot);
    assert.deepEqual(fragments.map((f) => [f.length, f.width]), [[2, 1], [1, 1]]);
    assert.equal(snapshot.positionAt(2).column, 1);
  });

  test('caller-supplied measurer with a standalone zero-width cluster', () => {
    // Caller reports base and combining mark as *separate* clusters;
    // the mark still occupies zero columns and never gets split.
    const measurer = {
      measure(segment: string): GraphemeMetric[] {
        return Array.from(segment, (ch): GraphemeMetric => ({
          length: ch.length,
          width: ch === '\u0301' ? 0 : ch === '你' ? 2 : 1,
        }));
      },
    };
    const snapshot = new LayoutEngine('e\u0301x你', { measurer }).snapshot();
    const widths = flattenFragments(snapshot).map((f) => f.width);
    assert.deepEqual(widths, [1, 0, 1, 2]);
    const x = snapshot.positionAt(2);
    assert.equal(x.column, 1);
    // The shared column has multiple logical candidates; hit-test
    // resolves by policy instead of faking a bijection.
    assert.equal(snapshot.hitTest(0, 0, 1, 'forward').offset, 1);
    assert.equal(snapshot.hitTest(0, 0, 1, 'backward').offset, 2);
  });
});

/* ---------------- soft wrap ---------------- */

describe('soft wrapping', () => {
  test('wide cluster moves to the next row instead of straddling', () => {
    const snapshot = new LayoutEngine('ab你', { wrapColumn: 3 }).snapshot();
    const line = snapshot.lines[0];
    assert.equal(line.height, 2);
    assert.equal(line.rows[0].fragments.length, 2);
    assert.equal(rowWidth(line.rows[0]), 2);
    assert.equal(line.rows[1].fragments[0].kind, 'cluster');
    assert.equal(line.rows[1].fragments[0].col, 0);
  });

  test('zero-width cluster never triggers a wrap alone', () => {
    const measurer = {
      measure(segment: string): GraphemeMetric[] {
        return Array.from(segment, (ch): GraphemeMetric => ({
          length: ch.length,
          width: ch === '\u0301' ? 0 : 1,
        }));
      },
    };
    const snapshot = new LayoutEngine('ab\u0301', { wrapColumn: 2, measurer }).snapshot();
    assert.equal(snapshot.lines[0].height, 1);
    assert.equal(snapshot.lines[0].rows[0].fragments.length, 3);
  });

  test('tab crossing the wrap column starts the next row', () => {
    const snapshot = new LayoutEngine('aaaa\tb', { wrapColumn: 4 }).snapshot();
    const line = snapshot.lines[0];
    assert.equal(line.height, 3);
    assert.equal(line.rows[1].fragments[0].kind, 'tab');
    assert.equal(line.rows[1].fragments[0].col, 0);
    assert.equal(line.rows[1].fragments[0].width, 4);
  });
});

/* ---------------- bidirectional conversion ---------------- */

describe('logical <-> visual conversion', () => {
  test('forward policy on second column of wide char lands on its start', () => {
    const engine = new LayoutEngine('你x');
    const forward = engine.snapshot().hitTest(0, 0, 1, 'forward');
    assert.equal(forward.offset, 0);
    assert.equal(forward.affinity, 'downstream');
    const backward = engine.snapshot().hitTest(0, 0, 1, 'backward');
    assert.equal(backward.offset, 1); // after the full 2-unit wide char
  });

  test('soft-wrap boundary maps one offset to two positions via affinity', () => {
    const engine = new LayoutEngine('ab你cd', { wrapColumn: 3 });
    const snapshot = engine.snapshot();
    // offset of 你 is 2; upstream visual pos = end of row 0,
    // downstream = start of row 1.
    const upstream = snapshot.positionAt(2, 'upstream');
    const downstream = snapshot.positionAt(2, 'downstream');
    assert.deepEqual([upstream.row, upstream.column], [0, 2]);
    assert.deepEqual([downstream.row, downstream.column], [1, 0]);
    // Both visual positions hit-test back to the same offset.
    assert.equal(snapshot.hitTest(0, 0, 2, 'backward').offset, 2);
    assert.equal(snapshot.hitTest(0, 1, 0).offset, 2);
  });

  test('offset inside a cluster snaps to the cluster start', () => {
    // e + combining acute accent form one cluster of UTF-16 length 2.
    const engine = new LayoutEngine('e\u0301b');
    assert.equal(engine.snapshot().positionAt(1).column, 0);
    assert.equal(engine.snapshot().positionAt(1).affinity, 'downstream');
  });

  test('nearest policy splits at the visual midpoint', () => {
    const snapshot = new LayoutEngine('你你').snapshot();
    assert.equal(snapshot.hitTest(0, 0, 0).offset, 0);
    assert.equal(snapshot.hitTest(0, 0, 1, 'nearest').offset, 1);
    assert.equal(snapshot.hitTest(0, 0, 2, 'nearest').offset, 1);
    assert.equal(snapshot.hitTest(0, 0, 3, 'nearest').offset, 2);
  });

  test('hit testing carries affinity at an ambiguous column', () => {
    const snapshot = new LayoutEngine('ab').snapshot();
    assert.equal(snapshot.hitTest(0, 0, 1, 'forward').affinity, 'upstream');
    assert.equal(snapshot.hitTest(0, 0, 1, 'backward').affinity, 'downstream');
  });
});

/* ---------------- UTF-16 / surrogate safety ---------------- */

describe('UTF-16 offsets', () => {
  const midPair = (text: string, offset: number) =>
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset));

  test('piece table never inserts inside a surrogate pair', () => {
    const table = new PieceTable('a🌍b');
    table.applyEdit(2, 0, 'X');
    assert.equal(table.getText(), 'aX🌍b');
    table.applyEdit(1, 1, '');
    assert.equal(table.getText(), 'a🌍b');
  });

  test('positionAt clamps an offset inside a surrogate pair', () => {
    const snapshot = new LayoutEngine('a🌍b').snapshot();
    const pos = snapshot.positionAt(2);
    assert.equal(pos.column, 1);
  });

  test('hitTest never returns an offset splitting a surrogate pair', () => {
    const engine = new LayoutEngine('x🌍y你z', { wrapColumn: 3 });
    const snapshot = engine.snapshot();
    for (const line of snapshot.lines) {
      for (let r = 0; r < line.rows.length; r += 1) {
        const width = rowWidth(line.rows[r]);
        for (let col = 0; col <= width + 1; col += 1) {
          for (const policy of ['forward', 'backward', 'nearest'] as const) {
            const hit = snapshot.hitTest(line.index, r, col, policy);
            assert.ok(
              !midPair(snapshot.text, hit.offset),
              `offset ${hit.offset} splits a pair at (${line.index},${r},${col},${policy})`,
            );
          }
        }
      }
    }
  });
});

/* ---------------- LF and CRLF ---------------- */

describe('line terminators', () => {
  test('LF splits lines with terminator length 1', () => {
    const snapshot = new LayoutEngine('a\nb').snapshot();
    assert.equal(snapshot.lines.length, 2);
    assert.deepEqual(
      snapshot.lines.map((l) => [l.start, l.contentLength, l.terminatorLength]),
      [[0, 1, 1], [2, 1, 0]],
    );
  });

  test('CRLF is one terminator of length 2', () => {
    const snapshot = new LayoutEngine('a\r\nb').snapshot();
    assert.equal(snapshot.lines.length, 2);
    assert.deepEqual(
      snapshot.lines.map((l) => [l.start, l.contentLength, l.terminatorLength]),
      [[0, 1, 2], [3, 1, 0]],
    );
    assert.equal(snapshot.positionAt(3).column, 0);
  });

  test('mixed LF and CRLF', () => {
    const snapshot = new LayoutEngine('a\r\nb\nc').snapshot();
    assert.deepEqual(snapshot.lines.map((l) => l.terminatorLength), [2, 1, 0]);
  });
});

/* ---------------- partial measurement ---------------- */

describe('partial measurement updates', () => {
  test('single-line height change does not re-measure unrelated lines', () => {
    const engine = new LayoutEngine('aaaa\nbbbb\ncccc', { wrapColumn: 4 });
    const before = engine.snapshot();
    assert.equal(before.height, 3);

    // Insert at the start of line 1 so it wraps into two rows.
    engine.applyEdit(5, 0, 'bbbb');
    assert.deepEqual(engine.measuredLines, [1]);

    const after = engine.snapshot();
    assert.equal(after.height, 4);
    assert.equal(after.lines[1].height, 2);

    // Lines 0 and 2 were NOT re-measured: their row data is shared
    // by reference with the previous snapshot.
    assert.equal(after.lines[0].rows, before.lines[0].rows);
    assert.equal(after.lines[2].rows, before.lines[2].rows);
    // Line 2 shifted by the 4 inserted code units.
    assert.equal(after.lines[2].start, before.lines[2].start + 4);
  });

  test('inserting a newline only measures the affected line range', () => {
    const engine = new LayoutEngine('one\ntwo\nthree');
    const before = engine.snapshot();
    engine.applyEdit(5, 0, '\n'); // split "two" into "t" + "wo"
    assert.deepEqual(engine.measuredLines, [1, 2]);
    const after = engine.snapshot();
    assert.equal(after.lines.length, 4);
    assert.equal(after.lines[0].rows, before.lines[0].rows);
    assert.equal(after.lines[3].rows, before.lines[2].rows);
  });

  test('old snapshots remain valid for hit testing after edits', () => {
    const engine = new LayoutEngine('abc\ndef');
    const old = engine.snapshot();
    engine.applyEdit(0, 0, 'PREPENDED\n');
    // New layout sees the inserted line first.
    assert.equal(engine.snapshot().lines.length, 3);
    // Old layout still answers with old offsets.
    const hit = old.hitTest(1, 0, 1, 'nearest');
    assert.equal(hit.offset, 5); // 'e' in the old document
    assert.equal(old.text, 'abc\ndef');
  });
});

/* ---------------- fuzz: random edits vs full layout ---------------- */

function mulberry32(seed: number) {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

describe('random-edit fuzz against full layout', () => {
  const alphabet = ['a', 'b', '中', '你', '\t', '\n', '\r\n', '🌍', 'é', '\u0301', ' ', 'x'];

  for (const wrapColumn of [Infinity, 12, 3]) {
    test(`incremental layout matches full layout (wrap=${wrapColumn})`, () => {
      const rand = mulberry32(wrapColumn === Infinity ? 1 : wrapColumn * 100 + 7);
      const engine = new LayoutEngine('line one\n第二行 🌍 x\r\ntab\there', {
        wrapColumn,
      });
      for (let iter = 0; iter < 200; iter += 1) {
        const len = engine.text.length;
        const start = Math.floor(rand() * (len + 1));
        const deleteCount = Math.floor(rand() * Math.min(9, len - start + 1));
        let inserted = '';
        const pieces = Math.floor(rand() * 6);
        for (let p = 0; p < pieces; p += 1) {
          inserted += alphabet[Math.floor(rand() * alphabet.length)];
        }
        engine.applyEdit(start, deleteCount, inserted);

        const incremental = engine.snapshot();
        const reference = computeFullLayout(engine.text, { wrapColumn });
        assert.deepEqual(
          snapshotSignature(incremental),
          snapshotSignature(reference),
          `layout mismatch after edit #${iter}: [${start}, +${deleteCount}) <- ${JSON.stringify(inserted)}`,
        );
      }
    });
  }

  test('every hit-test offset stays on a surrogate boundary after fuzz', () => {
    const rand = mulberry32(99);
    const engine = new LayoutEngine('🌍a你\tb\ne\u0301x\r\n中中中', { wrapColumn: 5 });
    for (let iter = 0; iter < 50; iter += 1) {
      const len = engine.text.length;
      engine.applyEdit(
        Math.floor(rand() * (len + 1)),
        Math.floor(rand() * 4),
        ['🌍', '你', '\t', '\n', '\u0301', 'q'][Math.floor(rand() * 6)],
      );
    }
    const snapshot = engine.snapshot();
    const midPair = (offset: number) =>
      isHighSurrogate(snapshot.text.charCodeAt(offset - 1)) &&
      isLowSurrogate(snapshot.text.charCodeAt(offset));
    for (const line of snapshot.lines) {
      for (let r = 0; r < line.rows.length; r += 1) {
        const width = rowWidth(line.rows[r]);
        for (let col = 0; col <= width; col += 1) {
          for (const policy of ['forward', 'backward', 'nearest'] as const) {
            assert.ok(!midPair(snapshot.hitTest(line.index, r, col, policy).offset));
          }
        }
      }
    }
  });
});
