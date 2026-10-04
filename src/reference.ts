import { ColumnData } from './batch';
import { applyOp, compareSortKeys } from './compare';
import { BatchSpec, columnData } from './source';
import { Plan, QueryResult, Value } from './types';

interface Rec {
  rowId: number;
  rowVersion: number;
  seq: number;
  cols: Record<string, Value>;
}

function norm(v: Value | undefined): Value {
  return v === undefined ? null : v;
}

/**
 * Row-at-a-time eager reference implementation. It materializes every
 * column of every row up front (including all wide columns) and applies
 * the same three-valued filter and stable single-key sort semantics.
 * The pipeline under test must produce value-identical output while
 * reading far less data.
 */
export async function runReference(
  specs: BatchSpec[],
  plan: Plan,
): Promise<QueryResult> {
  const rows: Rec[] = [];
  let seq = 0;
  for (const spec of specs) {
    const narrowData: Record<string, ColumnData> = {};
    const specNarrow = spec.narrow || {};
    for (const name of Object.keys(specNarrow)) {
      narrowData[name] = columnData(specNarrow[name].values, specNarrow[name].nulls);
    }
    const wideData: Record<string, ColumnData> = {};
    const specWide = spec.wide || {};
    for (const name of Object.keys(specWide)) {
      wideData[name] = await specWide[name]({});
    }
    for (let i = 0; i < spec.rowIds.length; i++) {
      const cols: Record<string, Value> = {};
      for (const name of Object.keys(narrowData)) {
        const d = narrowData[name];
        cols[name] = d.nulls[i] === 1 ? null : d.values[i];
      }
      for (const name of Object.keys(wideData)) {
        const d = wideData[name];
        cols[name] = d.nulls[i] === 1 ? null : d.values[i];
      }
      rows.push({
        rowId: spec.rowIds[i],
        rowVersion: spec.rowVersions[i],
        seq: seq++,
        cols,
      });
    }
  }

  let out = rows;
  if (plan.filter) {
    const f = plan.filter;
    out = out.filter((r) => applyOp(f.op, norm(r.cols[f.col]), f.value) === true);
  }
  if (plan.sort) {
    const key = plan.sort.key;
    const dir = plan.sort.dir === 'desc' ? -1 : 1;
    out = out.slice().sort((x, y) => {
      const c = compareSortKeys(norm(x.cols[key]), norm(y.cols[key]));
      if (c !== 0) return c * dir;
      return x.seq - y.seq;
    });
  }

  return {
    columns: plan.project.slice(),
    rows: out.map((r) => ({
      rowId: r.rowId,
      rowVersion: r.rowVersion,
      values: plan.project.map((c) => norm(r.cols[c])),
    })),
  };
}
