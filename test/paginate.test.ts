import { test } from 'node:test';
import assert from 'node:assert/strict';
import { paginate, DEFAULT_PAGE_CONFIG, type PageConfig } from '../src/paginate.ts';
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
const WORD = 'aaaaaaaaaa'; // width 100 -> exactly one word per line

function paraOfLines(id: string, n: number, refOnFirstLine?: string): Document['blocks'][number] {
  const words = Array.from({ length: n }, () => WORD);
  if (refOnFirstLine) words[0] = `${WORD}[^${refOnFirstLine}]`;
  return { kind: 'paragraph', id, text: words.join(' ') };
}

function docOf(blocks: Document['blocks']): Document {
  return { id: 't', blocks, footnotes: {} };
}

test('two-line paragraph is never split 1/1 (min 2 lines per side)', () => {
  const doc = docOf([
    { kind: 'block', id: 'b1', height: 90 },
    paraOfLines('p1', 2),
  ]);
  const layout = paginate(doc, metrics, config, {});
  assert.equal(layout.degradations.length, 0);
  assert.equal(layout.pages.length, 2);
  assert.deepEqual(layout.pages[0].atoms.map((a) => a.blockId), ['b1']);
  assert.deepEqual(layout.pages[1].atoms.map((a) => a.blockId), ['p1', 'p1']);
});

test('widow/orphan minimum of 2 lines is enforced on splits', () => {
  const layout = paginate(docOf([paraOfLines('p1', 5)]), metrics, { ...config, pageCapacity: 35 }, {});
  assert.equal(layout.degradations.length, 0);
  assert.deepEqual(layout.pages.map((p) => p.atoms.length), [3, 2]);
});

test('unsatisfiable widow/orphan constraints degrade explicitly, never silently', () => {
  // 3 lines x 10, capacity 15: the paragraph fits no page and no split
  // satisfies min-2 on both sides, so relaxation is unavoidable.
  const layout = paginate(docOf([paraOfLines('p1', 3)]), metrics, { ...config, pageCapacity: 15 }, {});
  const relaxed = layout.degradations.filter((d) => d.kind === 'constraint-relaxed');
  assert.ok(relaxed.length >= 1, 'expected an explicit constraint-relaxed degradation');
  assert.match(relaxed[0].message, /widow-orphan/);
  for (const page of layout.pages) assert.ok(page.atoms.length > 0, 'no blank pages');
});

test('heading keeps with the next block (never last on a page)', () => {
  const doc = docOf([
    { kind: 'block', id: 'b1', height: 15 },
    { kind: 'heading', id: 'h1', text: 'H' },
    paraOfLines('p1', 1),
  ]);
  const layout = paginate(doc, metrics, { ...config, pageCapacity: 30 }, {});
  assert.equal(layout.degradations.length, 0);
  assert.equal(layout.pages.length, 2);
  assert.equal(layout.pages[0].atoms.at(-1)?.kind, 'block');
  assert.equal(layout.pages[1].atoms[0].kind, 'heading');
});

test('oversized unbreakable block is flagged as overflow, layout continues', () => {
  const doc = docOf([
    paraOfLines('p1', 1),
    { kind: 'block', id: 'fig', height: 150 },
    paraOfLines('p2', 1),
  ]);
  const layout = paginate(doc, metrics, config, {});
  const overflow = layout.degradations.find((d) => d.kind === 'block-overflow');
  assert.ok(overflow, 'expected explicit block-overflow degradation');
  assert.match(overflow.message, /fig/);
  assert.match(overflow.message, /150/);
  const overflowPage = layout.pages.find((p) => p.overflow);
  assert.ok(overflowPage);
  assert.ok(overflowPage.slack < 0);
  for (const page of layout.pages) assert.ok(page.atoms.length > 0, 'no blank pages');
});

test('footnote reserves space on its reference page and pushes body text', () => {
  const withFn = paginate(docOf([paraOfLines('p1', 12, 'fn1')]), metrics, config, { fn1: 30 });
  const without = paginate(docOf([paraOfLines('p1', 12)]), metrics, config, {});
  assert.deepEqual(without.pages.map((p) => p.atoms.length), [10, 2]);
  assert.deepEqual(withFn.pages.map((p) => p.atoms.length), [6, 6]);
  const page0 = withFn.pages[0];
  assert.deepEqual(page0.footnotes, [{ id: 'fn1', height: 30 }]);
  assert.equal(page0.used, 60 + 30 + config.footnoteSeparatorHeight);
});

test('footnote too tall for its reference page degrades explicitly', () => {
  const layout = paginate(docOf([paraOfLines('p1', 1, 'big')]), metrics, config, { big: 200 });
  const deg = layout.degradations.find((d) => d.kind === 'footnote-overflow');
  assert.ok(deg, 'expected explicit footnote-overflow degradation');
  assert.ok(layout.pages[0].overflow);
});

test('no blank pages are produced even when backtracking relaxes constraints', () => {
  const scenarios = [
    paginate(docOf([paraOfLines('p1', 3)]), metrics, { ...config, pageCapacity: 15 }, {}),
    paginate(
      docOf([{ kind: 'block', id: 'b', height: 150 }, paraOfLines('p1', 3)]),
      metrics,
      config,
      {},
    ),
  ];
  for (const layout of scenarios) {
    for (const page of layout.pages) assert.ok(page.atoms.length > 0);
  }
});
