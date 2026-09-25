import assert from 'node:assert/strict';
import type { HeaderSnapshot, HeaderState, GroupNode } from '../src/index.ts';
import { listGroupIds, type HeaderOp } from '../src/index.ts';

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function columnIdsInTree(root: GroupNode): string[] {
  const ids: string[] = [];
  const walk = (node: GroupNode): void => {
    for (const child of node.children) {
      if (child.kind === 'leaf') ids.push(child.columnId);
      else walk(child);
    }
  };
  walk(root);
  return ids;
}

export function groupChildrenCount(state: HeaderState, groupId: string): number {
  const walk = (node: GroupNode): number | null => {
    if (node.id === groupId) return node.children.length;
    for (const child of node.children) {
      if (child.kind === 'group') {
        const hit = walk(child);
        if (hit !== null) return hit;
      }
    }
    return null;
  };
  const hit = walk(state.root);
  if (hit === null) throw new Error(`unknown group ${groupId}`);
  return hit;
}

/** Structural invariants that must hold for every published snapshot. */
export function assertSnapshotInvariants(snapshot: HeaderSnapshot): void {
  // No degenerate spans anywhere (empty groups must not leave invalid spans).
  for (const cell of snapshot.cells) {
    assert.ok(cell.rowSpan >= 1, `${cell.id} rowSpan`);
    assert.ok(cell.colSpan >= 1, `${cell.id} colSpan`);
    assert.ok(cell.coveredColumnIds.length >= 1, `${cell.id} covers columns`);
  }
  // Grid fully covered, exactly one cell per slot.
  assert.equal(snapshot.grid.length, snapshot.rows);
  for (const row of snapshot.grid) {
    assert.equal(row.length, snapshot.columnCount);
    for (const id of row) {
      assert.ok(snapshot.cells.some((c) => c.id === id), `grid ref ${id}`);
    }
  }
  // Header columns and data column indices always agree.
  assert.equal(snapshot.visibleColumns.length, snapshot.columnCount);
  snapshot.visibleColumns.forEach((vc, i) => {
    assert.equal(vc.dataIndex, i);
    assert.ok(snapshot.cells.some((c) => c.id === vc.cellId));
  });
  // Bottom-row cells, left to right, are exactly the data columns.
  const bottomRow = snapshot.rows - 1;
  const bottomCells = snapshot.rows === 0
    ? []
    : snapshot.grid[bottomRow].filter(
        (id, i, arr) => i === 0 || arr[i - 1] !== id,
      );
  assert.deepEqual(
    bottomCells,
    snapshot.visibleColumns.map((vc) => vc.cellId),
  );
  // Keyboard tree only references real cells.
  const ids = new Set(snapshot.cells.map((c) => c.id));
  for (const [id, edge] of Object.entries(snapshot.keyboard)) {
    assert.ok(ids.has(id));
    for (const target of Object.values(edge)) {
      if (target !== null) assert.ok(ids.has(target), `${id} -> ${target}`);
    }
  }
}

export function randomOp(rand: () => number, state: HeaderState): HeaderOp {
  const columns = columnIdsInTree(state.root);
  const groups = listGroupIds(state.root).filter((id) => id !== state.root.id);
  const pick = <T>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];
  const kind = Math.floor(rand() * 5);
  switch (kind) {
    case 0:
      return { type: 'hide', columnIds: [pick(columns)] };
    case 1:
      return { type: 'show', columnIds: [pick(columns)] };
    case 2:
      return { type: 'collapse', groupId: pick(groups) };
    case 3:
      return { type: 'expand', groupId: pick(groups) };
    default: {
      const toGroupId = pick([state.root.id, ...groups]);
      const count = groupChildrenCount(state, toGroupId);
      return {
        type: 'moveColumn',
        columnId: pick(columns),
        toGroupId,
        index: Math.floor(rand() * (count + 1)),
      };
    }
  }
}
