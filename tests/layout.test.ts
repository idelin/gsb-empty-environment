import { test } from 'node:test';
import assert from 'node:assert/strict';

import { layoutClusters } from '../src/layout.ts';
import { buildClusters, flattenTree } from '../src/clusters.ts';
import { allLevelSequences, reorderReference, reorderByLevels } from '../src/reorder.ts';
import { fixedMetrics, graphemeTree, nestedTree, paragraphsTree, rtlTree } from '../src/fixtures.ts';
import type { TreeNode } from '../src/types.ts';

function layoutOf(root: TreeNode, width = 100) {
  const clusters = buildClusters(flattenTree(root), fixedMetrics());
  return { clusters, snapshot: layoutClusters(clusters, width, 1) };
}

test('nested fragment: visual order reverses rtl runs and the even-level island', () => {
  const { snapshot } = layoutOf(nestedTree());
  // logical a b A B X Y c -> visual a b X Y B A c
  // (rtl run AB renders B A; the level-2 ltr island XY keeps its ltr order)
  assert.deepEqual(snapshot.lines[0]!.boxes.map((b) => b.clusterIndex), [0, 1, 4, 5, 3, 2, 6]);
  assert.deepEqual(snapshot.lines[0]!.boxes.map((b) => b.text), ['a', 'b', 'X', 'Y', 'B', 'A', 'c']);
});

test('left/right are not logical left/right inside the rtl run', () => {
  const { snapshot } = layoutOf(nestedTree());
  const find = (index: number) => snapshot.lines[0]!.boxes.find((b) => b.clusterIndex === index)!;
  // rtl cluster A(index 2) occupies x=5..6: leading edge is its RIGHT side
  assert.equal(find(2).dir, 'rtl');
  assert.equal(find(2).x, 5);
  // ltr cluster a: leading edge LEFT
  assert.equal(find(0).dir, 'ltr');
  assert.equal(find(0).x, 0);
});

test('direction boundary gives one logical offset two distinct visual boundaries', () => {
  const { snapshot } = layoutOf(nestedTree());
  const at2 = snapshot.carets.filter((c) => c.offset === 2);
  assert.equal(at2.length, 2);
  const xs = at2.map((c) => c.x).sort((a, b) => a - b);
  assert.deepEqual(xs, [2, 6]);
  assert.deepEqual(at2.map((c) => c.affinity.edge).sort(), ['leading', 'trailing']);
  // same on the other side of the run: offset 6 sits between A(rtl) and c(ltr)
  const at6 = snapshot.carets.filter((c) => c.offset === 6);
  assert.equal(at6.length, 2);
  assert.deepEqual(at6.map((c) => c.x).sort((a, b) => a - b), [4, 6]);
});

test('plain ltr/rtl interiors expose only one physical caret per offset', () => {
  const { snapshot } = layoutOf(nestedTree());
  assert.equal(snapshot.carets.filter((c) => c.offset === 1).length, 1);
  assert.equal(snapshot.carets.filter((c) => c.offset === 5).length, 1);
});

test('soft wrap splits an rtl run; wrap offset has explicit upper/lower affinity', () => {
  const { snapshot } = layoutOf(rtlTree(), 3);
  // logical ABCDE (rtl): line0 = ABC visually C B A, line1 = DE visually E D
  assert.deepEqual(snapshot.lines[0]!.boxes.map((b) => b.clusterIndex), [2, 1, 0]);
  assert.deepEqual(snapshot.lines[1]!.boxes.map((b) => b.clusterIndex), [4, 3]);

  const at3 = snapshot.carets.filter((c) => c.offset === 3);
  assert.equal(at3.length, 2);
  const upper = at3.find((c) => c.lineBias === 'upper')!;
  const lower = at3.find((c) => c.lineBias === 'lower')!;
  assert.equal(upper.line, 0);
  assert.equal(upper.x, 0); // end of upper rtl line == left
  assert.equal(lower.line, 1);
  assert.equal(lower.x, 2); // start of lower rtl line == right
});

test('grapheme clusters are cursor atomic: no stop inside ZWJ family or flag', () => {
  const { clusters, snapshot } = layoutOf(graphemeTree());
  assert.deepEqual(clusters.map((c) => c.text), ['x', '👨‍👩‍👧', '🇺🇳', 'y']);
  assert.equal(snapshot.carets.length, 5);
  for (const cluster of clusters.slice(1, 3)) {
    assert.ok(cluster.text.length > 1, 'cluster spans multiple UTF-16 code units');
    assert.equal(cluster.width, 1, 'metrics treat the cluster as one unit');
  }
});

test('forced breaks: hard-break cluster sits in no line and keeps correct offsets', () => {
  const { clusters, snapshot } = layoutOf(paragraphsTree());
  assert.equal(clusters[2]!.hardBreak, true);
  assert.equal(snapshot.lines.length, 2);
  assert.deepEqual(snapshot.lineRanges, [
    { start: 0, end: 2 },
    { start: 3, end: 5 },
  ]);
  assert.deepEqual(snapshot.lines.map((l) => l.boxes.map((b) => b.text)), [['a', 'b'], ['c', 'd']]);
});

test('property: every layout line, independently, matches the reference permutation', () => {
  let cases = 0;
  for (const seq of allLevelSequences(4, 3)) {
    const letters = ['a', 'b', 'c', 'd'];
    const tree: TreeNode = {
      id: 'r',
      children: [{ id: 't', text: letters.join(''), level: 0 }],
    };
    const clusters = buildClusters(flattenTree(tree), fixedMetrics()).map((c, i) => ({
      ...c,
      level: seq[i]!,
      dir: (seq[i]! & 1) === 0 ? ('ltr' as const) : ('rtl' as const),
    }));
    const snapshot = layoutClusters(clusters, 100, 1);
    const visual = snapshot.lines[0]!.boxes.map((b) => b.clusterIndex);
    assert.deepEqual(visual, reorderReference(seq));
    assert.deepEqual(visual, reorderByLevels(seq));
    cases++;
  }
  assert.equal(cases, 4 ** 4);
});

test('property: x positions are strictly non-decreasing in visual caret order', () => {
  for (const seq of allLevelSequences(4, 2)) {
    const letters = ['a', 'b', 'c', 'd'];
    const clusters = buildClusters(flattenTree({
      id: 'r',
      children: [{ id: 't', text: letters.join(''), level: 0 }],
    }), fixedMetrics()).map((c, i) => ({
      ...c,
      level: seq[i]!,
      dir: (seq[i]! & 1) === 0 ? ('ltr' as const) : ('rtl' as const),
    }));
    const { carets } = layoutClusters(clusters, 100, 1);
    for (let i = 1; i < carets.length; i++) {
      assert.ok(carets[i]!.x >= carets[i - 1]!.x);
    }
  }
});
