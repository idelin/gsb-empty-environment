import { replaceAll, ReplaceResult } from '../src';
import { assert, describe, it, naiveConcat, naiveReplacementOffsets } from './helpers';

/**
 * Cross-check a ReplaceResult against the naive concatenation model:
 * text, replacement landing offsets, and old<->new position mapping.
 */
function checkAgainstNaiveModel(oldText: string, result: ReplaceResult): void {
  const { map, edits } = result;
  const expectedText = naiveConcat(oldText, edits);
  assert.strictEqual(result.text, expectedText, 'new text equals naive concatenation');
  assert.strictEqual(map.oldLength, oldText.length);
  assert.strictEqual(map.newLength, expectedText.length);

  // Every replacement lands exactly where the naive model puts it.
  const landing = naiveReplacementOffsets(oldText, edits);
  edits.forEach((edit, i) => {
    const mapped = map.oldRangeToNew({ start: edit.start, end: edit.end });
    assert.deepStrictEqual(mapped, landing[i], `edit ${i} range maps to naive offset`);
    assert.strictEqual(
      result.text.slice(mapped.start, mapped.end),
      edit.newText,
      `edit ${i} replacement text sits at the mapped range`,
    );
    // Inverse: the new range maps back to the original old range.
    assert.deepStrictEqual(map.newRangeToOld(mapped), { start: edit.start, end: edit.end });
  });

  // Boundary offsets round-trip through the mapping. Offsets untouched by any
  // edit round-trip identically under both biases; an edit boundary collapses
  // with its counterpart, so it must land within the spanned interval.
  const insideEdit = (p: number) => edits.some((e) => p > e.start && p < e.end);
  const atEditBoundary = (p: number) => edits.some((e) => p === e.start || p === e.end);
  for (let p = 0; p <= oldText.length; p += 1) {
    if (insideEdit(p)) continue;
    const lo = map.newToOld(map.oldToNew(p, 'before'), 'before');
    const hi = map.newToOld(map.oldToNew(p, 'after'), 'after');
    if (!atEditBoundary(p)) {
      assert.strictEqual(lo, p, `offset ${p} round-trips old->new->old (before)`);
      assert.strictEqual(hi, p, `offset ${p} round-trips old->new->old (after)`);
    } else {
      assert.ok(lo <= p && p <= hi, `edit boundary ${p} stays within [${lo}, ${hi}]`);
    }
  }
  // New-text offsets land inside the old-image interval of their source point.
  const insideNewEdit = (p: number) =>
    landing.some((r) => p > r.start && p < r.end);
  for (let p = 0; p <= expectedText.length; p += 1) {
    if (insideNewEdit(p)) continue;
    const o = map.newToOld(p);
    assert.ok(
      map.oldToNew(o, 'before') <= p && p <= map.oldToNew(o, 'after'),
      `new offset ${p} stays within the image of old offset ${o}`,
    );
  }

  // Characters outside any edit are preserved at their mapped offsets.
  for (let p = 0; p < oldText.length; p += 1) {
    if (edits.some((e) => p >= e.start && p < e.end)) continue;
    assert.strictEqual(
      result.text.charAt(map.oldToNew(p)),
      oldText.charAt(p),
      `kept char at old offset ${p} is preserved`,
    );
  }

  // Endpoints anchor the whole mapping.
  assert.strictEqual(map.oldToNew(0, 'before'), 0);
  assert.strictEqual(map.oldToNew(oldText.length, 'after'), expectedText.length);
  assert.deepStrictEqual(map.oldRangeToNew({ start: 0, end: oldText.length }), {
    start: 0,
    end: expectedText.length,
  });
}

describe('position mapping vs naive concatenation model', () => {
  it('plain multi-match replacement', () => {
    checkAgainstNaiveModel('foo bar foo', replaceAll('foo bar foo', /foo/, 'x'));
  });

  it('empty replacement (pure deletion)', () => {
    checkAgainstNaiveModel('axbxc', replaceAll('axbxc', /x/, ''));
  });

  it('adjacent matches stay distinct', () => {
    const result = replaceAll('ababab', /ab/, '-');
    assert.strictEqual(result.text, '---');
    assert.deepStrictEqual(result.matches.map((m) => [m.start, m.end]), [
      [0, 2],
      [2, 4],
      [4, 6],
    ]);
    checkAgainstNaiveModel('ababab', result);
    // Each adjacent replacement maps to its own slot in the new text.
    assert.deepStrictEqual(result.map.oldRangeToNew({ start: 0, end: 2 }), { start: 0, end: 1 });
    assert.deepStrictEqual(result.map.oldRangeToNew({ start: 2, end: 4 }), { start: 1, end: 2 });
    assert.deepStrictEqual(result.map.oldRangeToNew({ start: 4, end: 6 }), { start: 2, end: 3 });
  });

  it('zero-width matches (pure insertions)', () => {
    const result = replaceAll('ab', /(?:)/, '-');
    assert.strictEqual(result.text, '-a-b-');
    checkAgainstNaiveModel('ab', result);
    // Insertion point maps to either side of the inserted text by bias.
    assert.strictEqual(result.map.oldToNew(1, 'before'), 2);
    assert.strictEqual(result.map.oldToNew(1, 'after'), 3);
    assert.strictEqual(result.map.newToOld(2, 'before'), 1);
    // Kept characters land between the inserted dashes.
    assert.strictEqual(result.map.oldToNew(0, 'after'), 1);
    assert.strictEqual(result.text.charAt(result.map.oldToNew(0, 'after')), 'a');
  });

  it('before/after context references in replacement', () => {
    checkAgainstNaiveModel('abc def', replaceAll('abc def', /def/, '[$`|$\']'));
  });

  it('capture references with non-participating groups', () => {
    checkAgainstNaiveModel('b b', replaceAll('b b', /(a)|(b)/, '<$1>'));
  });

  it('growing and shrinking replacements shift later offsets', () => {
    const result = replaceAll('a.b.c', /\./, '...');
    assert.strictEqual(result.text, 'a...b...c');
    checkAgainstNaiveModel('a.b.c', result);
    // 'c' moved from old offset 4 to new offset 8.
    assert.strictEqual(result.map.oldToNew(4), 8);
    assert.strictEqual(result.map.newToOld(8), 4);
  });

  it('rejects offsets outside the mapped texts', () => {
    const result = replaceAll('ab', /a/, 'x');
    assert.throws(() => result.map.oldToNew(3), RangeError);
    assert.throws(() => result.map.newToOld(3), RangeError);
  });
});
