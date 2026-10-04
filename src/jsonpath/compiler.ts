import type { Expr, JsonValue, PathSegment, Query } from './ast.ts';
import { deepEqual, isObject } from './ast.ts';
import { parse } from './parser.ts';
import type { BudgetTracker } from './budget.ts';
import { canonical } from './path.ts';
import type { Path } from './path.ts';
import { QueryError } from './errors.ts';

/** 编译后的谓词：纯闭包，从不经过宿主 eval/Function。 */
export interface FilterPlan {
  pred: (node: JsonValue, root: JsonValue | undefined, budget?: BudgetTracker) => boolean;
  usesRoot: boolean;
}

export type CompiledSegment = Exclude<PathSegment, { kind: 'filter' }> | { kind: 'filter'; plan: FilterPlan };

export interface Program {
  source: string;
  segments: CompiledSegment[];
  /** 任一谓词引用 $（文档根）→ 该查询需要全局信息，流式执行时延期到根闭合。 */
  hasRootRef: boolean;
}

export function compile(source: string | Query, budget?: BudgetTracker): Program {
  const query: Query = typeof source === 'string' ? parse(source, budget) : source;
  const segments = query.segments.map(compileSegment);
  return {
    source: query.source,
    segments,
    hasRootRef: segments.some((s) => s.kind === 'filter' && s.plan.usesRoot),
  };
}

function compileSegment(seg: PathSegment): CompiledSegment {
  if (seg.kind === 'filter') return { kind: 'filter', plan: compileExpr(seg.expr) };
  return seg;
}

export function compileExpr(expr: Expr): FilterPlan {
  let usesRoot = false;
  (function scan(e: Expr) {
    if (e.kind === 'root') usesRoot = true;
    if ('left' in e) {
      scan(e.left);
      scan((e as { right: Expr }).right);
    } else if (e.kind === 'not') scan(e.expr);
    else if (e.kind === 'exists') scan(e.expr);
  })(expr);
  const pred = (node: JsonValue, root: JsonValue | undefined, budget?: BudgetTracker): boolean =>
    evalBool(expr, node, root, budget);
  return { pred, usesRoot };
}

/** 表达式求值；路径导航消耗 expand 预算，比较消耗 compare 预算。 */
function evalValue(e: Expr, node: JsonValue, root: JsonValue | undefined, budget?: BudgetTracker): JsonValue | undefined {
  switch (e.kind) {
    case 'lit':
      return e.value;
    case 'cur':
      return navigate(e.path, node, budget);
    case 'root':
      if (root === undefined) throw new QueryError('NO_ROOT', 'filter references $ but no root document is available');
      return navigate(e.path, root, budget);
    default:
      return evalBool(e, node, root, budget);
  }
}

function navigate(path: PathSegment[], from: JsonValue, budget?: BudgetTracker): JsonValue | undefined {
  let cur: JsonValue | undefined = from;
  for (const seg of path) {
    budget?.consume('expand');
    if (cur === undefined) return undefined;
    if (seg.kind === 'child') {
      cur = isObject(cur) && Object.prototype.hasOwnProperty.call(cur, seg.key) ? cur[seg.key] : undefined;
    } else if (seg.kind === 'index') {
      if (!Array.isArray(cur)) return undefined;
      const i = seg.index < 0 ? cur.length + seg.index : seg.index;
      cur = i >= 0 && i < cur.length ? cur[i] : undefined;
    } else {
      throw new QueryError('BAD_FILTER_PATH', `segment kind ${seg.kind} is not allowed in filter paths`);
    }
  }
  return cur;
}

function evalBool(e: Expr, node: JsonValue, root: JsonValue | undefined, budget?: BudgetTracker): boolean {
  switch (e.kind) {
    case 'and':
      return evalBool(e.left, node, root, budget) && evalBool(e.right, node, root, budget);
    case 'or':
      return evalBool(e.left, node, root, budget) || evalBool(e.right, node, root, budget);
    case 'not':
      return !evalBool(e.expr, node, root, budget);
    case 'exists': {
      const v = evalValue(e.expr, node, root, budget);
      return v !== undefined && v !== null && v !== false;
    }
    case 'cmp': {
      budget?.consume('compare');
      const l = evalValue(e.left, node, root, budget);
      const r = evalValue(e.right, node, root, budget);
      if (l === undefined || r === undefined) return false;
      switch (e.op) {
        case '==':
          return deepEqual(l, r);
        case '!=':
          return !deepEqual(l, r);
        case '<':
        case '<=':
        case '>':
        case '>=': {
          const bothNum = typeof l === 'number' && typeof r === 'number';
          const bothStr = typeof l === 'string' && typeof r === 'string';
          if (!bothNum && !bothStr) return false;
          if (e.op === '<') return l < r;
          if (e.op === '<=') return l <= r;
          if (e.op === '>') return l > r;
          return l >= r;
        }
      }
    }
    default:
      return false;
  }
}

/** 该段是否需要等到容器闭合（全局信息：负下标/负切片/反向步长）。 */
export function needsContainerEnd(seg: CompiledSegment): boolean {
  if (seg.kind === 'index') return seg.index < 0;
  if (seg.kind === 'slice') {
    return seg.step < 0 || (seg.start !== null && seg.start < 0) || (seg.end !== null && seg.end < 0);
  }
  return false;
}

export function describePath(path: Path): string {
  return canonical(path);
}
