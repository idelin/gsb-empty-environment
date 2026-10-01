import { test } from 'node:test';
import assert from 'node:assert/strict';
import { measureText } from '../src/metrics.ts';
import { breakPositions, measureLine, paragraphToNodes } from '../src/nodes.ts';
import { breakParagraph, comparePaths } from '../src/breaker.ts';
import { greedyBreak } from '../src/greedy.ts';
import { exhaustiveBreak } from '../src/exhaustive.ts';
import { applyEdit, createSession } from '../src/incremental.ts';
import {
  BASE_OPTIONS,
  GREEDY_TRAP_OPTIONS,
  HYPHENATION,
  PARAGRAPHS,
  TIE_FIXTURE,
} from '../src/fixtures.ts';
import type { BreakOptions } from '../src/types.ts';

const opts = (over: Partial<BreakOptions> = {}): BreakOptions => ({
  ...BASE_OPTIONS,
  ...over,
});

test('fixture glyph metrics are the only measurement source', () => {
  assert.equal(measureText('The'), 6 + 5 + 5);
  assert.equal(measureText('i'), 2);
  assert.equal(measureText('-'), 3);
});

test('node tree from fixtures: boxes, glue, hyphenation penalties', () => {
  const nodes = paragraphToNodes('beautiful dog', HYPHENATION);
  assert.deepEqual(
    nodes.map((n) => n.kind),
    ['box', 'penalty', 'box', 'penalty', 'box', 'glue', 'box'],
  );
  const positions = breakPositions(nodes);
  assert.ok(positions[0] === 0 && positions[positions.length - 1] === nodes.length);
  const hyphenLine = measureLine(nodes, 0, 2); // break at the penalty after 'beau'
  assert.equal(hyphenLine.hyphenated, true);
  assert.equal(hyphenLine.text, 'beau-');
  const noBreakLine = measureLine(nodes, 0, 3); // penalty interior: not taken
  assert.equal(noBreakLine.hyphenated, false);
  assert.equal(noBreakLine.text, 'beauti');
});

test('DP matches exhaustive optimum on small paragraphs', () => {
  const cases: Array<[string, BreakOptions]> = [
    [PARAGRAPHS.small, opts({ lineWidth: 100 })],
    [PARAGRAPHS.small, opts({ lineWidth: 80, widthDiffWeight: 5 })],
    ['international optimization beautiful', opts({ lineWidth: 100, maxHyphens: 1 })],
    ['international optimization beautiful', opts({ lineWidth: 100, maxHyphens: 0 })],
    ['paragraph hyphenation optimization', opts({ lineWidth: 90, maxHyphens: 3 })],
  ];
  for (const [text, o] of cases) {
    const nodes = paragraphToNodes(text, HYPHENATION);
    const dp = breakParagraph(nodes, o);
    const ex = exhaustiveBreak(nodes, o);
    assert.equal(dp.status, 'ok');
    assert.equal(dp.optimalCost, ex.optimalCost, `cost mismatch for "${text}"`);
    assert.deepEqual(dp.breaks, ex.breaks, `breaks mismatch for "${text}"`);
  }
});

test('equal-cost layouts: earlier break positions win', () => {
  const { nodes, options } = TIE_FIXTURE;
  const ex = exhaustiveBreak(nodes, options);
  assert.ok(ex.optimalLayoutCount >= 2, 'fixture must have multiple optimal layouts');
  const dp = breakParagraph(nodes, options);
  assert.equal(dp.optimalCost, ex.optimalCost);
  assert.deepEqual(dp.breaks, ex.breaks, 'DP must pick the lexicographically earliest breaks');
  assert.ok(comparePaths([3, 9], [4, 5]) < 0);
});

test('greedy trap: global optimum beats local first-fit', () => {
  const nodes = paragraphToNodes(PARAGRAPHS.greedyTrap, {});
  const greedy = greedyBreak(nodes, GREEDY_TRAP_OPTIONS);
  const dp = breakParagraph(nodes, GREEDY_TRAP_OPTIONS);
  assert.match(greedy.heuristic, /NOT optimal/);
  assert.ok(
    (dp.optimalCost ?? Infinity) < greedy.totalCost,
    `DP ${dp.optimalCost} must beat greedy ${greedy.totalCost}`,
  );
  assert.deepEqual(dp.breaks, [0, 8, 9]);
});

test('unbreakable word wider than the line: terminates, overflow reported', () => {
  const nodes = paragraphToNodes(PARAGRAPHS.longWord, HYPHENATION);
  const r = breakParagraph(nodes, opts({ lineWidth: 60 }));
  assert.equal(r.status, 'ok');
  const overflowLines = r.lines.filter((l) => l.overflow);
  assert.equal(overflowLines.length, 1);
  assert.ok(overflowLines[0].atomic, 'overflow line must be the unbreakable word');
  assert.ok(overflowLines[0].remaining < 0, 'negative remaining width reported');
  assert.ok(r.warnings.some((w) => w.includes('unbreakable word wider than the line')));
});

test('cross-line hyphen budget is enforced', () => {
  const nodes = paragraphToNodes(PARAGRAPHS.hyphenHeavy, HYPHENATION);
  const r = breakParagraph(nodes, opts({ maxHyphens: 1 }));
  const hyphenated = r.lines.filter((l) => l.hyphenated);
  assert.ok(hyphenated.length <= 1, `used ${hyphenated.length} hyphens, budget 1`);
  const r2 = breakParagraph(nodes, opts({ maxHyphens: 2 }));
  assert.ok(r2.lines.filter((l) => l.hyphenated).length <= 2);
});

test('no two consecutive hyphenated lines', () => {
  const nodes = paragraphToNodes(PARAGRAPHS.hyphenHeavy, HYPHENATION);
  const r = breakParagraph(nodes, opts({ maxHyphens: 8 }));
  for (let i = 1; i < r.lines.length; i++) {
    assert.ok(
      !(r.lines[i - 1].hyphenated && r.lines[i].hyphenated),
      `lines ${i - 1} and ${i} both hyphenated`,
    );
  }
});

test('adjacent width-difference penalty influences the layout', () => {
  const nodes = paragraphToNodes(PARAGRAPHS.simple, HYPHENATION);
  const free = breakParagraph(nodes, opts({ widthDiffWeight: 0 }));
  const stiff = breakParagraph(nodes, opts({ widthDiffWeight: 50 }));
  const spread = (r: typeof free): number => {
    let s = 0;
    for (let i = 1; i < r.lines.length; i++) {
      s += Math.abs(r.lines[i].naturalWidth - r.lines[i - 1].naturalWidth);
    }
    return s;
  };
  assert.ok(spread(stiff) <= spread(free), 'higher weight must not increase width spread');
});

test('incremental edit reuses frontier and matches full recompute', () => {
  const nodes = paragraphToNodes(PARAGRAPHS.simple, HYPHENATION);
  const session = createSession(nodes, BASE_OPTIONS);
  const lazyIndex = nodes.findIndex((n) => n.kind === 'box' && n.text === 'lazy');
  const edit = applyEdit(session, lazyIndex, lazyIndex + 1, paragraphToNodes('energetic', HYPHENATION));
  assert.ok(edit.reuse.reusedStates > 0, 'must reuse unaffected frontier states');
  const fresh = breakParagraph(session.nodes, BASE_OPTIONS);
  assert.equal(edit.result.optimalCost, fresh.optimalCost);
  assert.deepEqual(edit.result.breaks, fresh.breaks);
});

test('incremental edit at the start degenerates to full recompute', () => {
  const nodes = paragraphToNodes(PARAGRAPHS.simple, HYPHENATION);
  const session = createSession(nodes, BASE_OPTIONS);
  const edit = applyEdit(session, 0, 1, paragraphToNodes('A', HYPHENATION));
  const fresh = breakParagraph(session.nodes, BASE_OPTIONS);
  assert.equal(edit.result.optimalCost, fresh.optimalCost);
  assert.deepEqual(edit.result.breaks, fresh.breaks);
});

test('budget exhaustion is reported separately, never as optimal', () => {
  const nodes = paragraphToNodes(PARAGRAPHS.simple, HYPHENATION);
  const r = breakParagraph(nodes, opts({ budget: 5 }));
  assert.equal(r.status, 'budget-exhausted');
  assert.equal(r.optimalCost, null);
  assert.ok(r.warnings.some((w) => w.includes('budget exhausted')));
  assert.equal(r.lines.length, 0);
});

test('certificate cumulative costs sum to the optimal cost', () => {
  const nodes = paragraphToNodes(PARAGRAPHS.simple, HYPHENATION);
  const r = breakParagraph(nodes, BASE_OPTIONS);
  assert.equal(r.status, 'ok');
  const sum = r.lines.reduce((n, l) => n + l.total, 0);
  assert.equal(sum, r.optimalCost);
  assert.equal(r.lines[r.lines.length - 1].cumulativeCost, r.optimalCost);
  assert.equal(r.certificate.length, r.lines.length + 1);
});

test('decision evidence marks exactly one chosen candidate per line', () => {
  const nodes = paragraphToNodes(PARAGRAPHS.simple, HYPHENATION);
  const r = breakParagraph(nodes, BASE_OPTIONS);
  assert.equal(r.evidence.length, r.lines.length);
  for (const e of r.evidence) {
    assert.equal(e.candidates.filter((c) => c.chosen).length, 1);
  }
});
