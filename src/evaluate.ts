import type {
  CmpOp,
  Database,
  Predicate,
  Query,
  RelInstance,
  RelationSpec,
  Value,
} from "./types.ts";
import { compareValues, tupleKey } from "./util.ts";

export class QueryError extends Error {}

export function sameAttrs(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((x, i) => x === b[i]);
}

function compareWith(op: CmpOp, left: Value, right: Value): boolean {
  const c = compareValues(left, right);
  switch (op) {
    case "eq":
      return c === 0;
    case "ne":
      return c !== 0;
    case "lt":
      return c < 0;
    case "le":
      return c <= 0;
    case "gt":
      return c > 0;
    case "ge":
      return c >= 0;
  }
}

export function evalPredicate(
  p: Predicate,
  attrs: readonly string[],
  row: readonly Value[],
): boolean {
  switch (p.kind) {
    case "cmp": {
      const i = attrs.indexOf(p.attr);
      if (i < 0) throw new QueryError(`选择谓词引用了不存在的属性: ${p.attr}`);
      if (p.value !== undefined && p.attr2 !== undefined) {
        throw new QueryError(`比较谓词不能同时给出 value 和 attr2(属性 ${p.attr})`);
      }
      let right: Value;
      if (p.attr2 !== undefined) {
        const j = attrs.indexOf(p.attr2);
        if (j < 0) throw new QueryError(`选择谓词引用了不存在的属性: ${p.attr2}`);
        const v = row[j];
        if (v === undefined) throw new QueryError(`行数据缺失属性: ${p.attr2}`);
        right = v;
      } else if (p.value !== undefined) {
        right = p.value;
      } else {
        throw new QueryError(`比较谓词缺少 value 或 attr2(属性 ${p.attr})`);
      }
      const left = row[i];
      if (left === undefined) throw new QueryError(`行数据缺失属性: ${p.attr}`);
      return compareWith(p.cmp, left, right);
    }
    case "and":
      return p.args.every((q) => evalPredicate(q, attrs, row));
    case "or":
      return p.args.some((q) => evalPredicate(q, attrs, row));
    case "not":
      return !evalPredicate(p.arg, attrs, row);
  }
}

export function evalQuery(
  query: Query,
  schema: ReadonlyMap<string, RelationSpec>,
  db: Database,
): RelInstance {
  switch (query.op) {
    case "rel": {
      const spec = schema.get(query.name);
      if (!spec) throw new QueryError(`未知关系: ${query.name}`);
      const inst = db.get(query.name);
      return {
        attrs: spec.attributes.map((a) => a.name),
        rows: new Map(inst ? inst.rows : []),
      };
    }
    case "select": {
      const input = evalQuery(query.input, schema, db);
      const rows = new Map<string, Value[]>();
      for (const [key, row] of input.rows) {
        if (evalPredicate(query.predicate, input.attrs, row)) rows.set(key, row);
      }
      return { attrs: input.attrs, rows };
    }
    case "project": {
      const input = evalQuery(query.input, schema, db);
      const seen = new Set<string>();
      const indexes = query.attributes.map((name) => {
        if (seen.has(name)) throw new QueryError(`投影属性重复: ${name}`);
        seen.add(name);
        const i = input.attrs.indexOf(name);
        if (i < 0) {
          throw new QueryError(`投影属性不存在: ${name}(可用: ${input.attrs.join(", ")})`);
        }
        return i;
      });
      const rows = new Map<string, Value[]>();
      for (const row of input.rows.values()) {
        const t = indexes.map((i) => row[i] as Value);
        rows.set(tupleKey(t), t);
      }
      return { attrs: [...query.attributes], rows };
    }
    case "union":
    case "minus": {
      const left = evalQuery(query.left, schema, db);
      const right = evalQuery(query.right, schema, db);
      if (!sameAttrs(left.attrs, right.attrs)) {
        const opName = query.op === "union" ? "并集" : "差集";
        throw new QueryError(
          `${opName}两边模式不兼容: [${left.attrs.join(", ")}] 与 [${right.attrs.join(", ")}]`,
        );
      }
      if (query.op === "union") {
        const rows = new Map(left.rows);
        for (const [key, row] of right.rows) rows.set(key, row);
        return { attrs: left.attrs, rows };
      }
      const rows = new Map<string, Value[]>();
      for (const [key, row] of left.rows) {
        if (!right.rows.has(key)) rows.set(key, row);
      }
      return { attrs: left.attrs, rows };
    }
  }
}
