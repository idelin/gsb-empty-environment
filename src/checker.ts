/**
 * 有界状态枚举器：对商状态图做 BFS。
 *
 * 三种互斥结论：
 *  - counterexample：发现违反安全性质的状态，BFS 保证轨迹步数最短；
 *  - proof：队列耗尽且未触及任何截断界 ⇒ 状态空间已饱和，
 *    结合 canon.ts 的互模拟论证，对任意长度、任意任期的执行成立；
 *  - bounded：因 maxSteps / maxStates 截断而停止，仅说明界内未发现反例，
 *    不构成任何证明。
 */

import { canonical, serialize } from './canon.ts';
import { initialState, successors, violation } from './model.ts';
import type { Options, State } from './model.ts';

export interface CheckLimits {
  maxSteps?: number;  // BFS 深度界（执行步数界）
  maxStates?: number; // 已发现状态数上限（资源保护）
}

export interface TraceEntry {
  label: string; // 到达该状态所执行的动作（初始状态为 '初始状态'）
  state: State;
}

export type CheckResult =
  | { type: 'counterexample'; explored: number; violation: string; trace: TraceEntry[] }
  | { type: 'proof'; explored: number; maxDepth: number }
  | { type: 'bounded'; explored: number; reason: string };

export function check(opts: Options, limits: CheckLimits = {}): CheckResult {
  const init = canonical(initialState());
  const initKey = serialize(init);

  const visited = new Set<string>([initKey]);
  const stateOf = new Map<string, State>([[initKey, init]]);
  const parent = new Map<string, { prev: string; label: string }>();

  // 用 head 指针代替 shift()，避免 O(n^2)
  const queue: { k: string; depth: number }[] = [{ k: initKey, depth: 0 }];
  let head = 0;
  let explored = 0;
  let maxDepth = 0;
  let truncated: string | null = null;

  while (head < queue.length) {
    const { k, depth } = queue[head++];
    explored++;
    if (depth > maxDepth) maxDepth = depth;

    if (limits.maxSteps !== undefined && depth >= limits.maxSteps) {
      truncated = `达到步数界 maxSteps=${limits.maxSteps}`;
      continue;
    }

    const s = stateOf.get(k)!;
    for (const { label, state: next } of successors(s, opts)) {
      const cs = canonical(next);
      const ck = serialize(cs);
      if (visited.has(ck)) continue;
      visited.add(ck);
      stateOf.set(ck, cs);
      parent.set(ck, { prev: k, label });

      const v = violation(cs);
      if (v !== null) {
        return {
          type: 'counterexample',
          explored,
          violation: v,
          trace: buildTrace(ck, initKey, parent, stateOf),
        };
      }

      queue.push({ k: ck, depth: depth + 1 });
      if (limits.maxStates !== undefined && visited.size > limits.maxStates) {
        return { type: 'bounded', explored, reason: `超过状态数上限 maxStates=${limits.maxStates}` };
      }
    }
  }

  if (truncated !== null) {
    return { type: 'bounded', explored, reason: truncated };
  }
  return { type: 'proof', explored, maxDepth };
}

function buildTrace(
  leaf: string,
  root: string,
  parent: Map<string, { prev: string; label: string }>,
  stateOf: Map<string, State>,
): TraceEntry[] {
  const entries: TraceEntry[] = [];
  let k = leaf;
  while (k !== root) {
    const p = parent.get(k)!;
    entries.push({ label: p.label, state: stateOf.get(k)! });
    k = p.prev;
  }
  entries.push({ label: '初始状态', state: stateOf.get(root)! });
  return entries.reverse();
}
