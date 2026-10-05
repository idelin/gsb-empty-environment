import { evalQuery, outputAttrs, SourceRow, tupleKey } from './eval';
import { enumerateDatabases, totalRows } from './enumerate';
import { Database, Query, RelationSchema, Row } from './types';

/** One output tuple on which the two queries disagree. */
export interface TupleDifference {
  /** Which query produces the tuple (the other one does not). */
  side: 'left' | 'right';
  tuple: Row;
  /** Base-relation rows that caused this tuple to appear on `side`. */
  sources: SourceRow[];
}

export interface Counterexample {
  db: Database;
  totalRows: number;
  leftResult: Row[];
  rightResult: Row[];
  differences: TupleDifference[];
}

export interface SearchResult {
  /** True when no distinguishing database with at most maxRows rows exists. */
  equivalentWithinBound: boolean;
  maxRows: number;
  databasesExamined: number;
  counterexample?: Counterexample;
}

function sortedRows(set: Map<string, { tuple: Row }>): Row[] {
  return [...set.values()]
    .map((e) => e.tuple)
    .sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
}

function sortedSources(lineage: Map<string, SourceRow>): SourceRow[] {
  return [...lineage.values()].sort((a, b) => {
    if (a.relation !== b.relation) return a.relation.localeCompare(b.relation);
    return JSON.stringify(a.row).localeCompare(JSON.stringify(b.row));
  });
}

/**
 * Search for the smallest database (first by total row count, then
 * lexicographically over the universe ordering) on which `left` and `right`
 * return different results under set semantics.
 */
export function findDifference(
  schemas: RelationSchema[],
  left: Query,
  right: Query,
  maxRows: number,
): SearchResult {
  const schemaMap = new Map(schemas.map((s) => [s.name, s]));
  const leftAttrs = outputAttrs(left, schemaMap);
  const rightAttrs = outputAttrs(right, schemaMap);
  if (leftAttrs.length !== rightAttrs.length || !leftAttrs.every((a) => rightAttrs.includes(a))) {
    throw new Error(
      `queries must have the same output schema, got [${leftAttrs}] and [${rightAttrs}]`,
    );
  }

  let examined = 0;
  for (const db of enumerateDatabases(schemas, maxRows)) {
    examined++;
    const leftSet = evalQuery(left, db, schemaMap);
    const rightSet = evalQuery(right, db, schemaMap);

    const differences: TupleDifference[] = [];
    for (const [key, e] of leftSet) {
      if (!rightSet.has(key)) {
        differences.push({ side: 'left', tuple: e.tuple, sources: sortedSources(e.lineage) });
      }
    }
    for (const [key, e] of rightSet) {
      if (!leftSet.has(key)) {
        differences.push({ side: 'right', tuple: e.tuple, sources: sortedSources(e.lineage) });
      }
    }

    if (differences.length > 0) {
      differences.sort((a, b) => {
        const ka = tupleKey(a.tuple, a.side === 'left' ? leftAttrs : rightAttrs);
        const kb = tupleKey(b.tuple, b.side === 'left' ? leftAttrs : rightAttrs);
        return ka.localeCompare(kb);
      });
      return {
        equivalentWithinBound: false,
        maxRows,
        databasesExamined: examined,
        counterexample: {
          db,
          totalRows: totalRows(db),
          leftResult: sortedRows(leftSet),
          rightResult: sortedRows(rightSet),
          differences,
        },
      };
    }
  }

  return { equivalentWithinBound: true, maxRows, databasesExamined: examined };
}
