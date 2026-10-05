import type { Database, RelationSpec, Value } from "./types.ts";
import { tupleKey } from "./util.ts";

export interface UniverseRow {
  rel: string;
  tuple: Value[];
}

function* cartesian(domains: readonly (readonly Value[])[]): Generator<Value[]> {
  if (domains.length === 0) {
    yield [];
    return;
  }
  const [first, ...rest] = domains;
  if (!first) return;
  for (const v of first) {
    for (const tail of cartesian(rest)) {
      yield [v, ...tail];
    }
  }
}

// 全集顺序:关系按声明顺序,元组按各属性域顺序做字典序笛卡尔积。
export function buildUniverse(relations: readonly RelationSpec[]): UniverseRow[] {
  const universe: UniverseRow[] = [];
  for (const rel of relations) {
    const domains = rel.attributes.map((a) => a.domain);
    for (const tuple of cartesian(domains)) {
      universe.push({ rel: rel.name, tuple });
    }
  }
  return universe;
}

// 按索引列表的字典序枚举 [0, n) 的所有 k 元子集。
export function* combinations(n: number, k: number): Generator<number[]> {
  if (k < 0 || k > n) return;
  const idx = Array.from({ length: k }, (_, i) => i);
  for (;;) {
    yield [...idx];
    let i = k - 1;
    while (i >= 0 && idx[i] === n - k + i) i--;
    if (i < 0) return;
    idx[i] = (idx[i] as number) + 1;
    for (let j = i + 1; j < k; j++) idx[j] = (idx[j - 1] as number) + 1;
  }
}

export function emptyDatabase(relations: readonly RelationSpec[]): Database {
  const db: Database = new Map();
  for (const rel of relations) {
    db.set(rel.name, {
      attrs: rel.attributes.map((a) => a.name),
      rows: new Map(),
    });
  }
  return db;
}

export function buildDatabase(
  relations: readonly RelationSpec[],
  universe: readonly UniverseRow[],
  indices: readonly number[],
): Database {
  const db = emptyDatabase(relations);
  for (const i of indices) {
    const u = universe[i];
    if (!u) continue;
    const inst = db.get(u.rel);
    if (inst) inst.rows.set(tupleKey(u.tuple), u.tuple);
  }
  return db;
}

function binom(n: number, k: number): number {
  if (k < 0 || k > n) return 0;
  const kk = Math.min(k, n - k);
  let r = 1;
  for (let i = 0; i < kk; i++) {
    r = (r * (n - i)) / (i + 1);
    if (r > 1e15) return Infinity;
  }
  return r;
}

export function countDatabases(universeSize: number, maxRows: number): number {
  let total = 0;
  for (let k = 0; k <= Math.min(maxRows, universeSize); k++) {
    total += binom(universeSize, k);
    if (total > 1e15) return Infinity;
  }
  return total;
}
