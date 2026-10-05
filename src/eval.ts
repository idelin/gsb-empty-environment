import { Database, Predicate, Query, RelationSchema, Row, Value } from './types';

/** Identifies one row of a base relation that contributed to an output tuple. */
export interface SourceRow {
  relation: string;
  row: Row;
}

/** Lineage of an output tuple: id -> source row (a Map doubles as an ordered set). */
export type Lineage = Map<string, SourceRow>;

export function sourceId(relation: string, row: Row, attrs: string[]): string {
  return relation + '#' + tupleKey(row, attrs);
}

/** Canonical key of a tuple under a fixed attribute order. */
export function tupleKey(row: Row, attrs: string[]): string {
  return JSON.stringify(attrs.map((a) => row[a]));
}

interface TupleEntry {
  tuple: Row;
  lineage: Lineage;
}

/** Result of a query under set semantics: tuple key -> entry. */
type TupleSet = Map<string, TupleEntry>;

function operandValue(op: { attr?: string; const?: Value }, row: Row): Value {
  if ('attr' in op && op.attr !== undefined) {
    return row[op.attr];
  }
  return (op as { const: Value }).const;
}

export function evalPredicate(pred: Predicate, row: Row): boolean {
  switch (pred.op) {
    case 'and':
      return pred.args.every((p) => evalPredicate(p, row));
    case 'or':
      return pred.args.some((p) => evalPredicate(p, row));
    case 'not':
      return !evalPredicate(pred.arg, row);
    default: {
      const l = operandValue(pred.left, row);
      const r = operandValue(pred.right, row);
      switch (pred.op) {
        case 'eq': return l === r;
        case 'ne': return l !== r;
        case 'lt': return l < r;
        case 'le': return l <= r;
        case 'gt': return l > r;
        case 'ge': return l >= r;
      }
    }
  }
}

export class SchemaError extends Error {}

/** Output attribute order of a query, with validation against the schemas. */
export function outputAttrs(q: Query, schemas: Map<string, RelationSchema>): string[] {
  switch (q.kind) {
    case 'relation': {
      const s = schemas.get(q.name);
      if (!s) throw new SchemaError(`unknown relation "${q.name}"`);
      return s.attrs.slice();
    }
    case 'select': {
      const attrs = outputAttrs(q.input, schemas);
      validatePredicate(q.where, attrs);
      return attrs;
    }
    case 'project': {
      const attrs = outputAttrs(q.input, schemas);
      for (const a of q.attrs) {
        if (!attrs.includes(a)) {
          throw new SchemaError(`project: attribute "${a}" not in input schema [${attrs}]`);
        }
      }
      if (q.attrs.length === 0) throw new SchemaError('project: empty attribute list');
      return q.attrs.slice();
    }
    case 'union':
    case 'difference': {
      const la = outputAttrs(q.left, schemas);
      const ra = outputAttrs(q.right, schemas);
      if (la.length !== ra.length || !la.every((a) => ra.includes(a))) {
        throw new SchemaError(
          `${q.kind}: operand schemas must match, got [${la}] and [${ra}]`,
        );
      }
      return la;
    }
  }
}

function validatePredicate(pred: Predicate, attrs: string[]): void {
  switch (pred.op) {
    case 'and':
    case 'or':
      pred.args.forEach((p) => validatePredicate(p, attrs));
      return;
    case 'not':
      validatePredicate(pred.arg, attrs);
      return;
    default:
      for (const side of [pred.left, pred.right]) {
        if ('attr' in side && side.attr !== undefined && !attrs.includes(side.attr)) {
          throw new SchemaError(`predicate: attribute "${side.attr}" not in schema [${attrs}]`);
        }
      }
  }
}

function mergeLineage(target: Lineage, src: Lineage): void {
  for (const [id, s] of src) target.set(id, s);
}

/**
 * Evaluate a query under set semantics. Every output tuple carries the set of
 * base-relation rows that contributed to it (lineage / provenance).
 */
export function evalQuery(
  q: Query,
  db: Database,
  schemas: Map<string, RelationSchema>,
): TupleSet {
  switch (q.kind) {
    case 'relation': {
      const schema = schemas.get(q.name);
      if (!schema) throw new SchemaError(`unknown relation "${q.name}"`);
      const out: TupleSet = new Map();
      for (const row of db[q.name] ?? []) {
        const key = tupleKey(row, schema.attrs);
        const lineage: Lineage = new Map();
        lineage.set(sourceId(q.name, row, schema.attrs), { relation: q.name, row });
        out.set(key, { tuple: row, lineage });
      }
      return out;
    }
    case 'select': {
      const input = evalQuery(q.input, db, schemas);
      const out: TupleSet = new Map();
      for (const [key, e] of input) {
        if (evalPredicate(q.where, e.tuple)) out.set(key, e);
      }
      return out;
    }
    case 'project': {
      const input = evalQuery(q.input, db, schemas);
      const out: TupleSet = new Map();
      for (const e of input.values()) {
        const tuple: Row = {};
        for (const a of q.attrs) tuple[a] = e.tuple[a];
        const key = tupleKey(tuple, q.attrs);
        const existing = out.get(key);
        if (existing) {
          mergeLineage(existing.lineage, e.lineage);
        } else {
          const lineage: Lineage = new Map();
          mergeLineage(lineage, e.lineage);
          out.set(key, { tuple, lineage });
        }
      }
      return out;
    }
    case 'union': {
      const left = evalQuery(q.left, db, schemas);
      const right = evalQuery(q.right, db, schemas);
      const out: TupleSet = new Map();
      for (const side of [left, right]) {
        for (const [key, e] of side) {
          const existing = out.get(key);
          if (existing) mergeLineage(existing.lineage, e.lineage);
          else out.set(key, { tuple: e.tuple, lineage: new Map(e.lineage) });
        }
      }
      return out;
    }
    case 'difference': {
      const left = evalQuery(q.left, db, schemas);
      const right = evalQuery(q.right, db, schemas);
      const out: TupleSet = new Map();
      for (const [key, e] of left) {
        if (!right.has(key)) out.set(key, e);
      }
      return out;
    }
  }
}

export type { TupleSet, TupleEntry };
