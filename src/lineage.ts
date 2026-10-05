import type { Query, RelationSpec, Value } from "./types.ts";
import { buildDatabase, type UniverseRow } from "./enumerate.ts";
import { evalQuery } from "./evaluate.ts";
import { tupleKey } from "./util.ts";

export interface SourceRow {
  rel: string;
  tuple: Value[];
  inDatabase: boolean;
  affectsQ1: boolean;
  affectsQ2: boolean;
}

// 翻转敏感性:对全集中的每一行,切换其"在/不在数据库"状态,
// 若差异行在 Q1 或 Q2 结果中的成员资格因此改变,则该行是来源行。
export function findSourceRows(
  relations: readonly RelationSpec[],
  schema: ReadonlyMap<string, RelationSpec>,
  q1: Query,
  q2: Query,
  universe: readonly UniverseRow[],
  dbIndices: readonly number[],
  diffTuple: readonly Value[],
): SourceRow[] {
  const key = tupleKey(diffTuple);
  const inDb = new Set(dbIndices);
  const has = (indices: readonly number[], q: Query): boolean =>
    evalQuery(q, schema, buildDatabase(relations, universe, indices)).rows.has(key);

  const base1 = has(dbIndices, q1);
  const base2 = has(dbIndices, q2);
  const sources: SourceRow[] = [];
  for (let i = 0; i < universe.length; i++) {
    const toggled = inDb.has(i)
      ? dbIndices.filter((j) => j !== i)
      : [...dbIndices, i].sort((a, b) => a - b);
    const t1 = has(toggled, q1);
    const t2 = has(toggled, q2);
    if (t1 !== base1 || t2 !== base2) {
      const u = universe[i];
      if (!u) continue;
      sources.push({
        rel: u.rel,
        tuple: u.tuple,
        inDatabase: inDb.has(i),
        affectsQ1: t1 !== base1,
        affectsQ2: t2 !== base2,
      });
    }
  }
  return sources;
}
