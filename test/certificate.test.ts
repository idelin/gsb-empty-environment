import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  attributeFootnotes,
  buildAtoms,
  paginate,
  DEFAULT_PAGE_CONFIG,
  type PageConfig,
} from '../src/paginate.ts';
import { buildCertificate, checkLayout, formatCertificate } from '../src/certificate.ts';
import { exhaustiveMinCost } from '../src/exhaustive.ts';
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

test('certificate lists per-page capacity, orphan checks and footnote attribution', () => {
  const layout = paginate(footnoteDoc(), metrics, config, { fn1: 30 });
  const cert = buildCertificate('t', layout, config);
  assert.equal(cert.pageCount, 2);
  assert.equal(cert.pages[0].capacity, 100);
  assert.deepEqual(cert.footnoteAttribution, { fn1: 0 });
  for (const page of cert.pages) {
    assert.ok(page.checks.nonEmpty);
    assert.ok(page.checks.withinCapacity);
    assert.ok(page.checks.widowsOrphans);
    assert.ok(page.checks.keepWithNext);
    assert.ok(page.checks.footnoteAttribution);
  }
  const text = formatCertificate(cert);
  assert.match(text, /capacity=100/);
  assert.match(text, /fn1->page 0/);
  assert.match(text, /degradations: none/);
});

test('checkLayout independently flags constraint violations', () => {
  const layout = paginate(footnoteDoc(), metrics, config, { fn1: 30 });
  // Forge a bad layout: move the footnote to the wrong page.
  const forged = structuredClone(layout);
  forged.pages[1].footnotes = [{ id: 'fn1', height: 30 }];
  forged.pages[0].footnotes = [];
  const checks = checkLayout(forged.pages, config);
  assert.equal(checks[1].footnoteAttribution, false);
});

test('exhaustive enumeration proves DP optimality on a small document', () => {
  const doc: Document = {
    id: 'small',
    blocks: [
      { kind: 'heading', id: 'h1', text: 'H' },
      {
        kind: 'paragraph',
        id: 'p1',
        text: [WORD, `${WORD}[^fn1]`, WORD, WORD].join(' '),
      },
      { kind: 'block', id: 'fig', height: 10 },
    ],
    footnotes: { fn1: { text: 'note' } },
  };
  const smallConfig = { ...config, pageCapacity: 50 };
  const heights = { fn1: 10 };
  const layout = paginate(doc, metrics, smallConfig, heights);
  assert.equal(layout.degradations.length, 0);
  const { atoms } = buildAtoms(doc, metrics);
  const exhaustive = exhaustiveMinCost(atoms, attributeFootnotes(atoms), heights, smallConfig);
  assert.ok(exhaustive);
  assert.ok(exhaustive.layouts >= 2, 'multiple candidate paginations exist');
  assert.equal(layout.cost, exhaustive.minCost, 'DP finds the optimum');

  const cert = buildCertificate('small', layout, smallConfig, {
    optimality: {
      verified: exhaustive.minCost === layout.cost,
      layoutsEnumerated: exhaustive.layouts,
      dpCost: layout.cost,
      exhaustiveMinCost: exhaustive.minCost,
    },
  });
  assert.ok(cert.optimality?.verified);
  const text = formatCertificate(cert);
  assert.match(text, /optimality: VERIFIED/);
});
