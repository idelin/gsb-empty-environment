import type { JsonValue } from './ast.ts';
import { isObject } from './ast.ts';
import type { Program } from './compiler.ts';
import type { BudgetTracker } from './budget.ts';
import { canonical } from './path.ts';
import type { Path } from './path.ts';

export interface Match {
  path: Path;
  value: JsonValue;
}

export interface EvalOptions {
  budget?: BudgetTracker;
  /** 谓词中 $ 的绑定；缺省为当前子树根。 */
  root?: JsonValue;
}

/** Python 语义的切片下标展开。 */
export function sliceIndices(length: number, start: number | null, end: number | null, step: number): number[] {
  const out: number[] = [];
  if (step > 0) {
    const s = start === null ? 0 : start < 0 ? Math.max(length + start, 0) : Math.min(start, length);
    const e = end === null ? length : end < 0 ? Math.max(length + end, 0) : Math.min(end, length);
    for (let i = s; i < e; i += step) out.push(i);
  } else {
    const s = start === null ? length - 1 : start < 0 ? Math.max(length + start, -1) : Math.min(start, length - 1);
    const e = end === null ? -1 : end < 0 ? Math.max(length + end, -1) : Math.min(end, length - 1);
    for (let i = s; i > e; i += step) out.push(i);
  }
  return out;
}

/**
 * 参考解释器（批式、全内存）。所有实现（流式/增量）都必须与它结果一致。
 * 全程迭代实现，深树不爆栈。
 */
export function evaluate(root: JsonValue, prog: Program, opts: EvalOptions = {}): Match[] {
  const byPath = new Map<string, Match>();
  evaluateFrom(root, [], prog, 0, { ...opts, root: opts.root ?? root }, (m) => {
    const key = canonical(m.path);
    if (!byPath.has(key)) byPath.set(key, m);
  });
  return [...byPath.values()];
}

/** 从子树 value（位于 basePath）执行 prog.segments[startSeg..]，结果经 emit 输出。 */
export function evaluateFrom(
  value: JsonValue,
  basePath: Path,
  prog: Program,
  startSeg: number,
  opts: EvalOptions,
  emit: (m: Match) => void,
): void {
  const segs = prog.segments;
  const budget = opts.budget;
  const root = opts.root ?? value;
  const stack: { node: JsonValue; path: Path; seg: number }[] = [{ node: value, path: basePath, seg: startSeg }];
  while (stack.length > 0) {
    const { node, path, seg } = stack.pop()!;
    budget?.consume('expand', 1, path);
    if (seg === segs.length) {
      emit({ path, value: node });
      continue;
    }
    const s = segs[seg]!;
    switch (s.kind) {
      case 'child':
        if (isObject(node) && Object.prototype.hasOwnProperty.call(node, s.key)) {
          stack.push({ node: node[s.key]!, path: [...path, s.key], seg: seg + 1 });
        }
        break;
      case 'wildcard':
        if (Array.isArray(node)) {
          for (let i = node.length - 1; i >= 0; i--) stack.push({ node: node[i]!, path: [...path, i], seg: seg + 1 });
        } else if (isObject(node)) {
          const keys = Object.keys(node);
          for (let i = keys.length - 1; i >= 0; i--) {
            stack.push({ node: node[keys[i]!]!, path: [...path, keys[i]!], seg: seg + 1 });
          }
        }
        break;
      case 'index':
        if (Array.isArray(node)) {
          const i = s.index < 0 ? node.length + s.index : s.index;
          if (i >= 0 && i < node.length) stack.push({ node: node[i]!, path: [...path, i], seg: seg + 1 });
        }
        break;
      case 'slice':
        if (Array.isArray(node)) {
          const idxs = sliceIndices(node.length, s.start, s.end, s.step);
          for (let k = idxs.length - 1; k >= 0; k--) {
            stack.push({ node: node[idxs[k]!]!, path: [...path, idxs[k]!], seg: seg + 1 });
          }
        }
        break;
      case 'filter':
        if (Array.isArray(node)) {
          for (let i = node.length - 1; i >= 0; i--) {
            if (s.plan.pred(node[i]!, root, budget)) stack.push({ node: node[i]!, path: [...path, i], seg: seg + 1 });
          }
        } else if (isObject(node)) {
          const keys = Object.keys(node);
          for (let i = keys.length - 1; i >= 0; i--) {
            const k = keys[i]!;
            if (s.plan.pred(node[k]!, root, budget)) stack.push({ node: node[k]!, path: [...path, k], seg: seg + 1 });
          }
        }
        break;
      case 'recursive': {
        // 自身 + 全部后代，迭代 DFS（先序：自身先弹出）。
        const desc: { node: JsonValue; path: Path }[] = [];
        const inner: { node: JsonValue; path: Path }[] = [{ node, path }];
        while (inner.length > 0) {
          const cur = inner.pop()!;
          if (Array.isArray(cur.node)) {
            for (let i = 0; i < cur.node.length; i++) {
              const cp = [...cur.path, i];
              desc.push({ node: cur.node[i]!, path: cp });
              inner.push({ node: cur.node[i]!, path: cp });
            }
          } else if (isObject(cur.node)) {
            for (const k of Object.keys(cur.node)) {
              const cp = [...cur.path, k];
              desc.push({ node: cur.node[k]!, path: cp });
              inner.push({ node: cur.node[k]!, path: cp });
            }
          }
        }
        for (let i = desc.length - 1; i >= 0; i--) stack.push({ node: desc[i]!.node, path: desc[i]!.path, seg: seg + 1 });
        stack.push({ node, path, seg: seg + 1 });
        break;
      }
    }
  }
}
