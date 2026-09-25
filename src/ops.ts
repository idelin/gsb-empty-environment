import type { JSONValue, Relation, Row, Tracked } from "./types.ts";
import { PathError } from "./errors.ts";
import { mergeEvidence } from "./lineage.ts";
import { field, rowEvidence } from "./relation.ts";

/** 值规范化序列化：对象键排序（键无序）、数组保序（数组有序），用于分组/连接键。 */
export function canonical(v: JSONValue): string {
  if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`;
  if (typeof v === "object" && v !== null) {
    const keys = Object.keys(v).sort();
    return `{${keys.map((k) => `${JSON.stringify(k)}:${canonical(v[k]!)}`).join(",")}}`;
  }
  return JSON.stringify(v) ?? "undefined";
}

/** 筛选：仅保留谓词为真的行；被过滤的行不进入下游，因而不出现在血缘中。 */
export function filterRel(
  rel: Relation,
  pred: (row: Row, index: number) => boolean,
): Relation {
  const rows: Row[] = [];
  for (let i = 0; i < rel.rows.length; i++) {
    const row = rel.rows[i]!;
    let keep: boolean;
    try {
      keep = pred(row, i);
    } catch (err) {
      if (err instanceof PathError) throw err;
      throw new PathError(`filter predicate failed: ${(err as Error).message}`, [i]);
    }
    if (keep) rows.push(row);
  }
  return { rows };
}

/** 拆分：按键把行分入多个 Relation；组与组内行均保持出现顺序（数组有序）。 */
export function splitRel(
  rel: Relation,
  keyOf: (row: Row, index: number) => string,
): Map<string, Relation> {
  const groups = new Map<string, Row[]>();
  for (let i = 0; i < rel.rows.length; i++) {
    const row = rel.rows[i]!;
    let key: string;
    try {
      key = keyOf(row, i);
    } catch (err) {
      if (err instanceof PathError) throw err;
      throw new PathError(`split key failed: ${(err as Error).message}`, [i]);
    }
    const bucket = groups.get(key);
    if (bucket === undefined) groups.set(key, [row]);
    else bucket.push(row);
  }
  return new Map([...groups.entries()].map(([k, rs]) => [k, { rows: rs }]));
}

export interface JoinSpec {
  /** 左、右用于等值连接的字段名。 */
  left: string;
  right: string;
}

/**
 * 内连接：左序保留；左右证据各自随字段流动。
 * 同名字段即连接键时合并为单字段、证据取双方并集；其余同名冲突报错。
 */
export function joinRel(left: Relation, right: Relation, spec: JoinSpec): Relation {
  const index = new Map<string, Row[]>();
  for (let j = 0; j < right.rows.length; j++) {
    const row = right.rows[j]!;
    const key = canonical(field(row, spec.right, [j, spec.right]).value);
    const bucket = index.get(key);
    if (bucket === undefined) index.set(key, [row]);
    else bucket.push(row);
  }
  const rows: Row[] = [];
  for (let i = 0; i < left.rows.length; i++) {
    const lrow = left.rows[i]!;
    const key = canonical(field(lrow, spec.left, [i, spec.left]).value);
    const matches = index.get(key);
    if (matches === undefined) continue;
    for (const rrow of matches) {
      const out: Row = { ...lrow };
      for (const [name, t] of Object.entries(rrow)) {
        if (name === spec.right && spec.right === spec.left && name in out) {
          out[name] = { value: out[name]!.value, evidence: mergeEvidence(out[name]!.evidence, t.evidence) };
        } else if (name in out) {
          throw new PathError(`join field collision on ${JSON.stringify(name)}`, [i, name]);
        } else {
          out[name] = t;
        }
      }
      rows.push(out);
    }
  }
  return { rows };
}

export type MeasureSpec =
  | { readonly op: "count" }
  | { readonly op: "sum" | "min" | "max"; readonly field: string }
  | { readonly op: "collect"; readonly field: string };

export interface AggregateSpec {
  readonly groupBy?: readonly string[];
  readonly measures: Readonly<Record<string, MeasureSpec>>;
}

function numeric(t: Tracked, at: (string | number)[]): number {
  if (typeof t.value !== "number") {
    const ev = t.evidence[0];
    if (ev !== undefined) {
      throw new PathError(`expected number, got ${JSON.stringify(t.value)}`, ev.path, ev.source);
    }
    throw new PathError(`expected number, got ${JSON.stringify(t.value)}`, at);
  }
  return t.value;
}

/**
 * 聚合：每个度量字段的证据 = 全部贡献行对应字段证据的并集，
 * 因此聚合输出可追到全部贡献行；同值不同来源的证据各自保留。
 */
export function aggregateRel(rel: Relation, spec: AggregateSpec): Relation {
  const groupBy = spec.groupBy ?? [];
  const groups = new Map<string, Row[]>();
  if (groupBy.length === 0) {
    groups.set("__all__", [...rel.rows]);
  } else {
    for (let i = 0; i < rel.rows.length; i++) {
      const row = rel.rows[i]!;
      const key = canonical(
        groupBy.map((g) => field(row, g, [i, g]).value) as unknown as JSONValue,
      );
      const bucket = groups.get(key);
      if (bucket === undefined) groups.set(key, [row]);
      else bucket.push(row);
    }
  }
  const rows: Row[] = [];
  for (const members of groups.values()) {
    const out: Row = {};
    for (const g of groupBy) {
      const ts = members.map((m) => field(m, g, [g]));
      out[g] = { value: ts[0]!.value, evidence: mergeEvidence(...ts.map((t) => t.evidence)) };
    }
    for (const [name, m] of Object.entries(spec.measures)) {
      switch (m.op) {
        case "count":
          out[name] = {
            value: members.length,
            evidence: mergeEvidence(...members.map(rowEvidence)),
          };
          break;
        case "sum":
        case "min":
        case "max": {
          const ts = members.map((r, k) => field(r, m.field, [k, m.field]));
          const nums = ts.map((t, k) => numeric(t, [k, m.field]));
          const value =
            m.op === "sum" ? nums.reduce((a, b) => a + b, 0)
            : m.op === "min" ? Math.min(...nums)
            : Math.max(...nums);
          out[name] = { value, evidence: mergeEvidence(...ts.map((t) => t.evidence)) };
          break;
        }
        case "collect": {
          const ts = members.map((r, k) => field(r, m.field, [k, m.field]));
          out[name] = {
            value: ts.map((t) => t.value),
            evidence: mergeEvidence(...ts.map((t) => t.evidence)),
          };
          break;
        }
      }
    }
    rows.push(out);
  }
  return { rows };
}
