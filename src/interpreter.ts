import type {
  Expr,
  JsonValue,
  Query,
  QueryResult,
  Segment,
  SimpleSegment,
} from "./ast.ts";
import type { Budget } from "./budget.ts";
import type { PathSeg } from "./path.ts";
import { encodePath } from "./path.ts";

export function isObject(value: JsonValue): value is { [key: string]: JsonValue } {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/** Iterative deep equality for JSON values (safe on very deep trees). */
export function jsonEqual(a: JsonValue, b: JsonValue): boolean {
  const stack: Array<[JsonValue, JsonValue]> = [[a, b]];
  while (stack.length > 0) {
    const [x, y] = stack.pop()!;
    if (x === y) continue;
    if (x === null || y === null || typeof x !== "object" || typeof y !== "object") {
      return false;
    }
    if (Array.isArray(x) || Array.isArray(y)) {
      if (!Array.isArray(x) || !Array.isArray(y) || x.length !== y.length) return false;
      for (let i = 0; i < x.length; i++) stack.push([x[i], y[i]]);
      continue;
    }
    const kx = Object.keys(x);
    const ky = Object.keys(y);
    if (kx.length !== ky.length) return false;
    for (const k of kx) {
      if (!Object.hasOwn(y, k)) return false;
      stack.push([x[k], (y as { [key: string]: JsonValue })[k]]);
    }
  }
  return true;
}

function truthy(value: JsonValue | undefined): boolean {
  return value !== undefined && value !== false && value !== null;
}

function compareValues(
  op: "==" | "!=" | "<" | "<=" | ">" | ">=",
  left: JsonValue | undefined,
  right: JsonValue | undefined,
): boolean {
  if (op === "==") {
    return left !== undefined && right !== undefined && jsonEqual(left, right);
  }
  if (op === "!=") {
    return !(left !== undefined && right !== undefined && jsonEqual(left, right));
  }
  if (typeof left === "number" && typeof right === "number") {
    switch (op) {
      case "<": return left < right;
      case "<=": return left <= right;
      case ">": return left > right;
      case ">=": return left >= right;
    }
  }
  if (typeof left === "string" && typeof right === "string") {
    switch (op) {
      case "<": return left < right;
      case "<=": return left <= right;
      case ">": return left > right;
      case ">=": return left >= right;
    }
  }
  return false;
}

/** Hand-written predicate evaluation; never touches eval/Function. */
export function evalExpr(
  expr: Expr,
  current: JsonValue,
  budget?: Budget,
): JsonValue | undefined {
  switch (expr.kind) {
    case "literal":
      return expr.value;
    case "current": {
      let value: JsonValue | undefined = current;
      for (const seg of expr.path) {
        if (value === undefined || value === null || typeof value !== "object") {
          return undefined;
        }
        if (Array.isArray(value)) {
          if (typeof seg !== "number") return undefined;
          value = value[seg];
        } else {
          if (typeof seg !== "string") return undefined;
          value = Object.hasOwn(value, seg) ? value[seg] : undefined;
        }
      }
      return value;
    }
    case "not":
      return !truthy(evalExpr(expr.operand, current, budget));
    case "and":
      return truthy(evalExpr(expr.left, current, budget)) &&
        truthy(evalExpr(expr.right, current, budget));
    case "or":
      return truthy(evalExpr(expr.left, current, budget)) ||
        truthy(evalExpr(expr.right, current, budget));
    case "cmp": {
      budget?.charge("compare");
      const left = evalExpr(expr.left, current, budget);
      const right = evalExpr(expr.right, current, budget);
      return compareValues(expr.op, left, right);
    }
  }
}

export function exprPasses(expr: Expr, current: JsonValue, budget?: Budget): boolean {
  return truthy(evalExpr(expr, current, budget));
}

/** Python-style slice index resolution. */
export function sliceIndices(
  seg: { start: number | null; end: number | null; step: number | null },
  length: number,
): number[] {
  const step = seg.step ?? 1;
  const out: number[] = [];
  if (step > 0) {
    const start = seg.start === null
      ? 0
      : seg.start < 0
        ? Math.max(length + seg.start, 0)
        : Math.min(seg.start, length);
    const end = seg.end === null
      ? length
      : seg.end < 0
        ? Math.max(length + seg.end, 0)
        : Math.min(seg.end, length);
    for (let i = start; i < end; i += step) out.push(i);
  } else {
    const start = seg.start === null
      ? length - 1
      : seg.start < 0
        ? Math.max(length + seg.start, 0)
        : Math.min(seg.start, length - 1);
    const end = seg.end === null
      ? -1
      : seg.end < 0
        ? Math.max(length + seg.end, -1)
        : seg.end;
    for (let i = start; i > end; i += step) out.push(i);
  }
  return out;
}

function childEntries(value: JsonValue): Array<[PathSeg, JsonValue]> {
  if (Array.isArray(value)) {
    return value.map((v, i) => [i, v] as [PathSeg, JsonValue]);
  }
  if (isObject(value)) {
    return Object.keys(value).map((k) => [k, value[k]] as [PathSeg, JsonValue]);
  }
  return [];
}

/** Children of `value` selected by one non-recursive segment, in document order. */
export function matchSegmentChildren(
  seg: SimpleSegment,
  value: JsonValue,
  path: PathSeg[],
  budget?: Budget,
): QueryResult[] {
  switch (seg.kind) {
    case "field": {
      if (isObject(value) && Object.hasOwn(value, seg.name)) {
        return [{ path: [...path, seg.name], value: value[seg.name] }];
      }
      return [];
    }
    case "index": {
      if (!Array.isArray(value)) return [];
      const i = seg.index < 0 ? seg.index + value.length : seg.index;
      if (i < 0 || i >= value.length) return [];
      return [{ path: [...path, i], value: value[i] }];
    }
    case "slice": {
      if (!Array.isArray(value)) return [];
      return sliceIndices(seg, value.length).map((i) => ({
        path: [...path, i],
        value: value[i],
      }));
    }
    case "wildcard":
      return childEntries(value).map(([k, v]) => ({ path: [...path, k], value: v }));
    case "filter":
      return childEntries(value)
        .filter(([, v]) => exprPasses(seg.expr, v, budget))
        .map(([k, v]) => ({ path: [...path, k], value: v }));
  }
}

interface WorkItem {
  value: JsonValue;
  path: PathSeg[];
  seg: number;
}

/**
 * Reference interpreter: evaluates `segments` starting at (`value`, `path`).
 * Fully iterative, so arbitrarily deep trees are safe. Results are deduped
 * by path (first occurrence wins) and returned in document order.
 */
export function evaluateSegments(
  value: JsonValue,
  path: PathSeg[],
  segments: Segment[],
  budget?: Budget,
): QueryResult[] {
  const results: QueryResult[] = [];
  const seen = new Set<string>();
  const emit = (p: PathSeg[], v: JsonValue): void => {
    const key = encodePath(p);
    if (!seen.has(key)) {
      seen.add(key);
      results.push({ path: p, value: v });
    }
  };
  const stack: WorkItem[] = [{ value, path: [...path], seg: 0 }];
  while (stack.length > 0) {
    const item = stack.pop()!;
    budget?.charge("expand");
    if (item.seg === segments.length) {
      emit(item.path, item.value);
      continue;
    }
    const seg = segments[item.seg];
    const pushAll = (matches: QueryResult[]): void => {
      for (let i = matches.length - 1; i >= 0; i--) {
        stack.push({ value: matches[i].value, path: matches[i].path, seg: item.seg + 1 });
      }
    };
    if (seg.kind === "recursive") {
      // Visit every node of the subtree (including the root node) and try the
      // inner selector against its children. Iterative walk, document order.
      const matches: WorkItem[] = [];
      const walk: Array<{ value: JsonValue; path: PathSeg[] }> = [
        { value: item.value, path: item.path },
      ];
      while (walk.length > 0) {
        const node = walk.pop()!;
        budget?.charge("expand");
        for (const m of matchSegmentChildren(seg.inner, node.value, node.path, budget)) {
          matches.push({ value: m.value, path: m.path, seg: item.seg + 1 });
        }
        const kids = childEntries(node.value);
        for (let i = kids.length - 1; i >= 0; i--) {
          walk.push({ value: kids[i][1], path: [...node.path, kids[i][0]] });
        }
      }
      for (let i = matches.length - 1; i >= 0; i--) stack.push(matches[i]);
      continue;
    }
    pushAll(matchSegmentChildren(seg, item.value, item.path, budget));
  }
  return results;
}

export function evaluate(
  root: JsonValue,
  query: Query,
  budget?: Budget,
): QueryResult[] {
  return evaluateSegments(root, [], query.segments, budget);
}
