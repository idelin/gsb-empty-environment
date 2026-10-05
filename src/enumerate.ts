import { Database, RelationSchema, Row } from './types';

/** One row of the universe: a relation name plus a concrete tuple. */
export interface UniverseRow {
  relation: string;
  row: Row;
}

/**
 * The universe of all possible rows: for every relation (in schema order),
 * the cartesian product of its attribute domains, ordered lexicographically
 * by attribute position with each domain kept in its declared order.
 */
export function buildUniverse(schemas: RelationSchema[]): UniverseRow[] {
  const universe: UniverseRow[] = [];
  for (const schema of schemas) {
    const tuples: Row[] = [{}];
    for (let i = 0; i < schema.attrs.length; i++) {
      const attr = schema.attrs[i];
      const domain = schema.domains[i];
      const next: Row[] = [];
      for (const partial of tuples) {
        for (const v of domain) {
          next.push({ ...partial, [attr]: v });
        }
      }
      tuples.length = 0;
      tuples.push(...next);
    }
    for (const row of tuples) universe.push({ relation: schema.name, row });
  }
  return universe;
}

/**
 * Yield all k-element index subsets of {0..n-1} in lexicographic order of the
 * sorted index list.
 */
function* combinations(n: number, k: number): Generator<number[]> {
  if (k < 0 || k > n) return;
  const idx = Array.from({ length: k }, (_, i) => i);
  while (true) {
    yield idx.slice();
    let i = k - 1;
    while (i >= 0 && idx[i] === n - k + i) i--;
    if (i < 0) return;
    idx[i]++;
    for (let j = i + 1; j < k; j++) idx[j] = idx[j - 1] + 1;
  }
}

/**
 * Enumerate all database instances whose total row count is at most maxRows.
 * Order: first by total row count (0, 1, 2, ...), then lexicographically by
 * the sorted list of universe indices the database contains.
 */
export function* enumerateDatabases(
  schemas: RelationSchema[],
  maxRows: number,
): Generator<Database> {
  const universe = buildUniverse(schemas);
  const n = universe.length;
  const limit = Math.min(maxRows, n);
  for (let k = 0; k <= limit; k++) {
    for (const combo of combinations(n, k)) {
      const db: Database = {};
      for (const s of schemas) db[s.name] = [];
      for (const i of combo) {
        const u = universe[i];
        db[u.relation].push(u.row);
      }
      yield db;
    }
  }
}

/** Total number of rows across all relations of a database. */
export function totalRows(db: Database): number {
  let total = 0;
  for (const name of Object.keys(db)) total += db[name].length;
  return total;
}
