import type { RelInstance, RelationSpec, Spec, Value } from "./types.ts";
import { QueryError, evalQuery, sameAttrs } from "./evaluate.ts";
import {
  buildDatabase,
  buildUniverse,
  combinations,
  countDatabases,
  emptyDatabase,
} from "./enumerate.ts";
import { findSourceRows, type SourceRow } from "./lineage.ts";
import { compareTuples } from "./util.ts";

export interface DifferenceEntry {
  tuple: Value[];
  side: "Q1" | "Q2";
  sourceRows: SourceRow[];
}

export interface RelationDump {
  rel: string;
  attrs: string[];
  rows: Value[][];
}

export interface DifferentResult {
  kind: "different";
  totalRows: number;
  databasesChecked: number;
  note?: string;
  database: RelationDump[];
  q1: { attrs: string[]; rows: Value[][] };
  q2: { attrs: string[]; rows: Value[][] };
  onlyInQ1: Value[][];
  onlyInQ2: Value[][];
  differences: DifferenceEntry[];
}

export interface EquivalentResult {
  kind: "equivalent";
  maxRows: number;
  exhaustive: boolean;
  databasesChecked: number;
}

export type SearchResult = DifferentResult | EquivalentResult;

export interface SearchOptions {
  maxDatabases?: number;
}

const DEFAULT_MAX_DATABASES = 2_000_000;

function validateSpec(spec: Spec): void {
  if (!Array.isArray(spec.relations) || spec.relations.length === 0) {
    throw new QueryError("spec.relations 必须是非空数组");
  }
  const names = new Set<string>();
  for (const rel of spec.relations) {
    if (!rel.name) throw new QueryError("关系缺少 name");
    if (names.has(rel.name)) throw new QueryError(`关系名重复: ${rel.name}`);
    names.add(rel.name);
    if (!Array.isArray(rel.attributes) || rel.attributes.length === 0) {
      throw new QueryError(`关系 ${rel.name} 至少需要一个属性`);
    }
    const attrNames = new Set<string>();
    for (const attr of rel.attributes) {
      if (attrNames.has(attr.name)) {
        throw new QueryError(`关系 ${rel.name} 属性名重复: ${attr.name}`);
      }
      attrNames.add(attr.name);
      if (!Array.isArray(attr.domain) || attr.domain.length === 0) {
        throw new QueryError(`关系 ${rel.name} 属性 ${attr.name} 的域必须非空`);
      }
    }
  }
  if (!Number.isInteger(spec.maxRows) || spec.maxRows < 0) {
    throw new QueryError("spec.maxRows 必须是非负整数");
  }
  if (!spec.queries || !spec.queries.Q1 || !spec.queries.Q2) {
    throw new QueryError("spec.queries 必须包含 Q1 和 Q2");
  }
}

function sortedRows(inst: RelInstance): Value[][] {
  return [...inst.rows.values()].sort(compareTuples);
}

function dumpDatabase(relations: readonly RelationSpec[], indices: readonly number[], universe: ReturnType<typeof buildUniverse>): RelationDump[] {
  const db = buildDatabase(relations, universe, indices);
  return relations.map((rel) => {
    const inst = db.get(rel.name);
    return {
      rel: rel.name,
      attrs: rel.attributes.map((a) => a.name),
      rows: inst ? sortedRows(inst) : [],
    };
  });
}

export function findDifference(spec: Spec, options: SearchOptions = {}): SearchResult {
  validateSpec(spec);
  const relations = spec.relations;
  const schema = new Map(relations.map((r) => [r.name, r]));
  const universe = buildUniverse(relations);
  const maxRows = Math.min(spec.maxRows, universe.length);
  const limit = options.maxDatabases ?? DEFAULT_MAX_DATABASES;
  const total = countDatabases(universe.length, maxRows);
  if (total > limit) {
    throw new QueryError(
      `搜索空间过大: 需检查约 ${total} 个数据库(上限 ${limit})。` +
        `请缩小属性域或 maxRows,或调大 maxDatabases。`,
    );
  }

  const empty = emptyDatabase(relations);
  const probe1 = evalQuery(spec.queries.Q1, schema, empty);
  const probe2 = evalQuery(spec.queries.Q2, schema, empty);
  if (!sameAttrs(probe1.attrs, probe2.attrs)) {
    return {
      kind: "different",
      totalRows: 0,
      databasesChecked: 1,
      note:
        `两个查询的结果模式不同: Q1 为 [${probe1.attrs.join(", ")}],` +
        `Q2 为 [${probe2.attrs.join(", ")}],空数据库即可区分。`,
      database: dumpDatabase(relations, [], universe),
      q1: { attrs: probe1.attrs, rows: [] },
      q2: { attrs: probe2.attrs, rows: [] },
      onlyInQ1: [],
      onlyInQ2: [],
      differences: [],
    };
  }

  let checked = 0;
  for (let k = 0; k <= maxRows; k++) {
    for (const combo of combinations(universe.length, k)) {
      checked++;
      const db = buildDatabase(relations, universe, combo);
      const r1 = evalQuery(spec.queries.Q1, schema, db);
      const r2 = evalQuery(spec.queries.Q2, schema, db);
      const only1: Value[][] = [];
      const only2: Value[][] = [];
      for (const [key, row] of r1.rows) if (!r2.rows.has(key)) only1.push(row);
      for (const [key, row] of r2.rows) if (!r1.rows.has(key)) only2.push(row);
      if (only1.length === 0 && only2.length === 0) continue;
      only1.sort(compareTuples);
      only2.sort(compareTuples);
      const differences: DifferenceEntry[] = [
        ...only1.map((tuple) => ({ tuple, side: "Q1" as const })),
        ...only2.map((tuple) => ({ tuple, side: "Q2" as const })),
      ].map((d) => ({
        ...d,
        sourceRows: findSourceRows(
          relations,
          schema,
          spec.queries.Q1,
          spec.queries.Q2,
          universe,
          combo,
          d.tuple,
        ),
      }));
      return {
        kind: "different",
        totalRows: k,
        databasesChecked: checked,
        database: dumpDatabase(relations, combo, universe),
        q1: { attrs: r1.attrs, rows: sortedRows(r1) },
        q2: { attrs: r2.attrs, rows: sortedRows(r2) },
        onlyInQ1: only1,
        onlyInQ2: only2,
        differences,
      };
    }
  }
  return {
    kind: "equivalent",
    maxRows,
    exhaustive: maxRows >= universe.length,
    databasesChecked: checked,
  };
}
