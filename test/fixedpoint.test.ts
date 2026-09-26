import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DEFAULT_PAGE_CONFIG, type PageConfig } from '../src/paginate.ts';
import {
  applyCorrection,
  constantOracle,
  paginateToFixedPoint,
  textMetricsOracle,
  type FootnoteOracle,
} from '../src/fixedpoint.ts';
import type { Document, GlyphMetrics } from '../src/types.ts';

const metrics: GlyphMetrics = {
  charWidths: {},
  defaultCharWidth: 10,
  spaceWidth: 5,
  lineHeight: 10,
  headingHeight: 10,
  maxLineWidth: 100,
};

const config: PageConfig = { ...DEFAULT_PAGE_CONFIG, pageCapacity: 100 };
const WORD = 'aaaaaaaaaa';

function footnoteDoc(): Document {
  return {
    id: 't',
    blocks: [
      {
        kind: 'paragraph',
        id: 'p1',
        text: Array.from({ length: 12 }, (_, i) => (i === 0 ? `${WORD}[^fn1]` : WORD)).join(' '),
      },
    ],
    footnotes: { fn1: { text: 'note' } },
  };
}

test('fixed point converges for constant footnote heights', () => {
  const fp = paginateToFixedPoint(footnoteDoc(), metrics, config, constantOracle({ fn1: 30 }));
  assert.ok(fp.ok);
  assert.equal(fp.iterations, 1);
  assert.deepEqual(fp.assignment, { fn1: 0 });
});

test('local content correction cascades: taller footnote pushes body lines', () => {
  const doc = footnoteDoc();
  const before = paginateToFixedPoint(doc, metrics, config, constantOracle({ fn1: 30 }));
  assert.ok(before.ok);
  assert.equal(before.layout.pages[0].atoms.length, 6);

  const corrected = applyCorrection(doc, { footnoteText: {} });
  const after = paginateToFixedPoint(corrected, metrics, config, constantOracle({ fn1: 60 }));
  assert.ok(after.ok);
  assert.equal(after.layout.pages[0].atoms.length, 3, 'taller footnote pushes 3 more lines off page 0');
  assert.equal(after.layout.pages[1].atoms.length, 9);
});

test('text-metrics oracle derives footnote heights from fixture metrics', () => {
  const doc = footnoteDoc();
  doc.footnotes.fn1 = { text: 'aaaa bbbb cccc dddd' }; // 2 lines at width 100
  const oracle = textMetricsOracle(doc, metrics, 100);
  assert.deepEqual(oracle.heights({ assignment: null, iteration: 0 }), { fn1: 20 });
});

test('oscillating footnote heights report no fixed point instead of looping', () => {
  const flipFlop: FootnoteOracle = {
    heights: ({ iteration }) => ({ fn1: iteration % 2 === 0 ? 20 : 10 }),
  };
  const fp = paginateToFixedPoint(footnoteDoc(), metrics, config, flipFlop);
  assert.equal(fp.ok, false);
  if (!fp.ok) {
    assert.match(fp.reason, /no fixed point/);
    assert.ok(fp.history.length > 0);
  }
});
