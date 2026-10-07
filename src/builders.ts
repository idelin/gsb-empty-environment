import type { BlockSpec, RowSpec, TableSpec } from "./types.ts";

let autoId = 0;

function nextId(prefix: string): string {
  autoId += 1;
  return `${prefix}-${autoId}`;
}

/** A single row. */
export function row(height: number, id?: string): RowSpec {
  return { id: id ?? nextId("row"), height };
}

/** A single-row, breakable-anywhere block. */
export function line(height: number, id?: string): BlockSpec {
  const r = row(height, id);
  return { id: r.id, rows: [r] };
}

/**
 * An unbreakable multi-row block: rows glued by a merged cell.
 * Accepts row heights or RowSpec objects.
 */
export function group(id: string, rows: Array<number | RowSpec>): BlockSpec {
  if (rows.length === 0) throw new Error(`Group "${id}" must contain at least one row`);
  return {
    id,
    rows: rows.map((r) => (typeof r === "number" ? row(r) : r)),
  };
}

export function table(headerHeight: number, blocks: BlockSpec[]): TableSpec {
  return { headerHeight, blocks };
}

export function blockHeight(block: BlockSpec): number {
  return block.rows.reduce((sum, r) => sum + r.height, 0);
}
