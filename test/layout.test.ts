import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  findByRole,
  layoutAnnouncement,
  measureText,
  wrapText,
  type GlyphMetrics,
} from '../src/layout.ts';
import { glyphMetrics, nodeTree } from '../fixtures/index.ts';

const metrics: GlyphMetrics = {
  defaultAdvance: 10,
  advances: { ' ': 6, i: 4, l: 4 },
};

test('measureText sums per-glyph advances with fallback', () => {
  assert.equal(measureText('hi', metrics), 14); // 10 + 4
  assert.equal(measureText('ll', metrics), 8); // 4 + 4
  assert.equal(measureText('a b', metrics), 26); // 10 + 6 + 10
  assert.equal(measureText('', metrics), 0);
});

test('findByRole locates the status region in the fixture node tree', () => {
  const status = findByRole(nodeTree, 'status');
  assert.ok(status);
  assert.equal(status.id, 'status-region');
  assert.equal(status.width, 160);
  assert.equal(findByRole(nodeTree, 'nonexistent'), undefined);
});

test('wrapText packs words greedily by measured width', () => {
  // "abc"(30) + " "(6) + "de"(20) = 56
  assert.deepEqual(wrapText('abc de', 60, metrics), ['abc de']);
  assert.deepEqual(wrapText('abc de', 50, metrics), ['abc', 'de']);
  assert.deepEqual(wrapText('abc de fg', 50, metrics), ['abc', 'de fg']); // "de fg" = 20+6+20=46
});

test('wrapText hard-splits words longer than the line width', () => {
  assert.deepEqual(wrapText('abcdef', 25, metrics), ['ab', 'cd', 'ef']);
});

test('layoutAnnouncement wraps into the fixture status region width', () => {
  const text = 'sync complete for twelve documents';
  const layout = layoutAnnouncement(nodeTree, text, glyphMetrics);
  assert.equal(layout.region.id, 'status-region');
  assert.ok(layout.lines.length >= 1);
  for (const line of layout.lines) {
    assert.ok(
      measureText(line, glyphMetrics) <= 160,
      `line "${line}" exceeds the status region width`,
    );
  }
  // No words lost or duplicated by wrapping.
  assert.deepEqual(layout.lines.join(' ').split(' '), text.split(' '));
});

test('layoutAnnouncement fails loudly when the fixture lacks a status region', () => {
  assert.throws(
    () => layoutAnnouncement({ id: 'root' }, 'hello', glyphMetrics),
    /role "status"/,
  );
});
