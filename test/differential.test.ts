import test from 'node:test';
import assert from 'node:assert/strict';
import { VirtualList, type RowInput } from '../src/virtualList.ts';
import { fixtureMetrics, textNode } from '../src/fixtures.ts';
import { mulberry32, randInt, pick } from '../src/rng.ts';
import {
  computeLayout,
  refCapture,
  refRestore,
  refVisibleIds,
  type RefLayout,
} from './reference.ts';
import type { RowId } from '../src/types.ts';

const EST = 50;
const VIEWPORT = 220;
const GROUPS = ['ga', 'gb', null, null] as const;

interface PendingMeasure {
  id: RowId;
  token: number;
  height: number;
}

function randomRow(rand: () => number, n: number): RowInput {
  return {
    id: `n${n}`,
    node: textNode(`row ${n}`),
    groupId: pick(rand, GROUPS),
  };
}

function runDifferential(seed: number, steps: number): void {
  const rand = mulberry32(seed);
  const initial: RowInput[] = [];
  for (let i = 0; i < 12; i++) initial.push(randomRow(rand, i));
  const list = new VirtualList(
    {
      estimatedHeight: EST,
      viewportHeight: VIEWPORT,
      measureWidth: 320,
      metrics: fixtureMetrics(),
    },
    initial,
  );
  list.scrollTo(randInt(rand, 0, 400));

  let nextRow = 12;
  const pending: PendingMeasure[] = [];

  const layoutOf = (): RefLayout =>
    computeLayout(list.getRows(), list.collapsedGroupIds(), list.measuredHeights(), EST);

  const check = (label: string): void => {
    const layout = layoutOf();
    assert.equal(list.totalHeight(), layout.total, `${label}: totalHeight`);
    assert.deepEqual(
      [...list.visibleRowIds()],
      layout.visibleIds,
      `${label}: visible sequence`,
    );
    assert.deepEqual(
      list.visibleRange().ids,
      refVisibleIds(layout, list.scrollTop, VIEWPORT),
      `${label}: visible set @ scrollTop=${list.scrollTop}`,
    );
  };

  for (let step = 0; step < steps; step++) {
    const beforeLayout = layoutOf();
    const scrollBefore = list.scrollTop;
    const savedAnchor = refCapture(beforeLayout, scrollBefore);
    const op = randInt(rand, 0, 9);
    let label = `step ${step}`;

    if (op === 0) {
      // insert 1-3 rows at a random model position
      const rows = Array.from({ length: randInt(rand, 1, 3) }, () => randomRow(rand, nextRow++));
      const at = randInt(rand, 0, list.size);
      label += ` insert@${at} [${rows.map((r) => r.id)}]`;
      list.insertRows(at, rows);
    } else if (op === 1 && list.size > 2) {
      // delete 1-2 random rows
      const ids: RowId[] = [];
      const rows = list.getRows();
      for (let k = 0; k < randInt(rand, 1, 2); k++) ids.push(pick(rand, rows).id);
      label += ` remove [${ids}]`;
      list.removeRows(ids);
    } else if (op === 2 && list.size > 1) {
      // reorder
      const rows = list.getRows();
      const id = pick(rand, rows).id;
      const to = randInt(rand, 0, list.size - 1);
      label += ` move ${id}->${to}`;
      list.moveRow(id, to);
    } else if (op === 3) {
      // collapse / expand a group
      const g = pick(rand, ['ga', 'gb']);
      const collapsed = !list.collapsedGroupIds().has(g);
      label += ` ${collapsed ? 'collapse' : 'expand'} ${g}`;
      list.setCollapsed(g, collapsed);
    } else if (op === 4 && list.size > 0) {
      // fresh measurement, applied immediately
      const rows = list.getRows();
      const id = pick(rand, rows).id;
      const token = list.requestMeasurement(id);
      const height = randInt(rand, 0, 4) === 0 ? 0 : randInt(rand, 10, 160);
      label += ` measure ${id}=${height}`;
      assert.equal(list.applyMeasurement(id, height, token), true, `${label}: applied`);
    } else if (op === 5 && list.size > 0) {
      // schedule a measurement; resolve an older pending one (possibly stale)
      const rows = list.getRows();
      const id = pick(rand, rows).id;
      pending.push({ id, token: list.requestMeasurement(id), height: randInt(rand, 10, 160) });
      if (pending.length > 0 && randInt(rand, 0, 1) === 1) {
        const idx = randInt(rand, 0, pending.length - 1);
        const [m] = pending.splice(idx, 1);
        label += ` late-measure ${m.id}=${m.height} tok=${m.token}`;
        list.applyMeasurement(m.id, m.height, m.token); // may be stale: must not corrupt
      }
    } else if (op === 6) {
      // scroll, both directions (reverse scrolling included)
      const delta = randInt(rand, -300, 300);
      label += ` scrollBy ${delta}`;
      list.scrollBy(delta);
      check(label);
      continue; // pure scroll: no anchor restore involved
    } else if (op === 7 && list.visibleCount > 0) {
      // jump to a random visible row
      const id = pick(rand, [...list.visibleRowIds()]);
      label += ` jump ${id}`;
      list.jumpToId(id, pick(rand, ['top', 'center'] as const));
      check(label);
      continue;
    } else if (op === 8) {
      // width change: invalidates in-flight measurements
      label += ' width-change';
      list.setMeasureWidth(pick(rand, [320, 120, 480]));
    } else {
      label += ' noop-scrollTo';
      list.scrollTo(randInt(rand, 0, 600));
      check(label);
      continue;
    }

    // Structural / height ops: scroll position must follow the public
    // fallback rules, verified against the independent reference.
    const afterLayout = layoutOf();
    const expectedScroll = refRestore(savedAnchor, beforeLayout, afterLayout, scrollBefore, VIEWPORT);
    assert.equal(list.scrollTop, expectedScroll, `${label}: scrollTop after op`);
    check(label);
  }
}

test('differential: random ops vs full-layout reference (multiple seeds)', () => {
  for (const seed of [1, 7, 42, 1337, 90210]) {
    runDifferential(seed, 400);
  }
});
