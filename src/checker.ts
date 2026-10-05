/**
 * 兼容性检查器：
 * 把新版协议（A）与旧版协议（B）作为两个对端，通过两条容量受限的 FIFO
 * 队列（A→B、B→A）组合起来，BFS 探索全部可达全局状态。
 *
 * 判定结果：
 *   - compatible            所有会话都能正常终止（双方到达终止态且队列清空）
 *   - unexpected-reception  某方队首消息在当前状态无任何接收迁移可消费
 *   - deadlock              无任何可执行动作且未正常终止
 *                           （区分“双方都在等待”与“一方已终止另一方空等”）
 *   - non-terminating       存在可达全局状态，从它出发永远无法正常终止（活锁）
 *
 * BFS 保证报告的错误轨迹是最短的。
 */

import { Action, IndexedProtocol, Transition } from "./protocol";

export type Party = "A" | "B";

export interface GlobalState {
  readonly a: string;
  readonly b: string;
  /** A→B 方向队列（队首在下标 0） */
  readonly qAB: readonly string[];
  /** B→A 方向队列 */
  readonly qBA: readonly string[];
}

export interface Step {
  readonly party: Party;
  readonly action: Action;
  readonly from: GlobalState;
  readonly to: GlobalState;
}

export type DeadlockCategory = "both-waiting" | "peer-terminated" | "stuck";

export interface DeadlockInfo {
  readonly category: DeadlockCategory;
  readonly description: string;
}

export type Verdict =
  | { readonly kind: "compatible"; readonly explored: number; readonly terminalCount: number }
  | {
      readonly kind: "unexpected-reception";
      readonly party: Party;
      readonly message: string;
      readonly state: GlobalState;
      readonly trace: readonly Step[];
    }
  | {
      readonly kind: "deadlock";
      readonly info: DeadlockInfo;
      readonly state: GlobalState;
      readonly trace: readonly Step[];
    }
  | {
      readonly kind: "non-terminating";
      readonly state: GlobalState;
      readonly trace: readonly Step[];
      readonly cycle: readonly Step[] | null;
    };

export const DEFAULT_CAPACITY = 2;

function keyOf(s: GlobalState): string {
  return `${s.a}|${s.b}|${s.qAB.join(",")}|${s.qBA.join(",")}`;
}

interface PartyView {
  readonly proto: IndexedProtocol;
  readonly local: string;
  readonly incoming: readonly string[];
  readonly outgoing: readonly string[];
}

function viewsOf(s: GlobalState, pa: IndexedProtocol, pb: IndexedProtocol): Record<Party, PartyView> {
  return {
    A: { proto: pa, local: s.a, incoming: s.qBA, outgoing: s.qAB },
    B: { proto: pb, local: s.b, incoming: s.qAB, outgoing: s.qBA },
  };
}

function isEnabled(view: PartyView, t: Transition, capacity: number): boolean {
  switch (t.action.kind) {
    case "send":
      return view.outgoing.length < capacity;
    case "recv":
      return view.incoming[0] === t.action.message;
    case "internal":
      return true;
  }
}

function applyTransition(s: GlobalState, party: Party, t: Transition): GlobalState {
  const msg = t.action.message;
  if (party === "A") {
    switch (t.action.kind) {
      case "send":
        return { ...s, a: t.to, qAB: [...s.qAB, msg!] };
      case "recv":
        return { ...s, a: t.to, qBA: s.qBA.slice(1) };
      case "internal":
        return { ...s, a: t.to };
    }
  }
  switch (t.action.kind) {
    case "send":
      return { ...s, b: t.to, qBA: [...s.qBA, msg!] };
    case "recv":
      return { ...s, b: t.to, qAB: s.qAB.slice(1) };
    case "internal":
      return { ...s, b: t.to };
  }
}

function classifyDeadlock(state: GlobalState, pa: IndexedProtocol, pb: IndexedProtocol): DeadlockInfo {
  const describe = (local: string, p: IndexedProtocol) => {
    const out = p.outgoing.get(local) ?? [];
    return {
      waiting: out.some((t) => t.action.kind === "recv"),
      isFinal: p.protocol.finals.includes(local),
    };
  };
  const A = describe(state.a, pa);
  const B = describe(state.b, pb);
  if (A.waiting && B.waiting) {
    return { category: "both-waiting", description: "双方都在等待对方发送消息（双向等待）" };
  }
  if (A.waiting || B.waiting) {
    const waitingSide = A.waiting ? pa.protocol.name : pb.protocol.name;
    const otherSide = A.waiting ? pb.protocol.name : pa.protocol.name;
    const otherFinal = A.waiting ? B.isFinal : A.isFinal;
    return {
      category: otherFinal ? "peer-terminated" : "stuck",
      description: otherFinal
        ? `${otherSide} 已到达终止状态，而 ${waitingSide} 仍在等待永远不会到来的消息`
        : `${waitingSide} 在等待消息，而 ${otherSide} 已无任何可执行动作`,
    };
  }
  return { category: "stuck", description: "双方都没有可执行动作，且未同时处于终止状态" };
}

export function checkCompatibility(
  newProto: IndexedProtocol,
  oldProto: IndexedProtocol,
  capacity: number = DEFAULT_CAPACITY
): Verdict {
  if (capacity < 1) throw new Error("队列容量至少为 1");
  const initial: GlobalState = {
    a: newProto.protocol.initial,
    b: oldProto.protocol.initial,
    qAB: [],
    qBA: [],
  };
  const startKey = keyOf(initial);

  const states = new Map<string, GlobalState>([[startKey, initial]]);
  const parent = new Map<string, { prevKey: string; step: Step }>();
  const depth = new Map<string, number>([[startKey, 0]]);
  const edges = new Map<string, string[]>();
  const edgeStep = new Map<string, Step>();
  const terminalKeys: string[] = [];
  const queue: string[] = [startKey];

  const buildTrace = (key: string): Step[] => {
    const steps: Step[] = [];
    let k = key;
    for (let p = parent.get(k); p; p = parent.get(k)) {
      steps.push(p.step);
      k = p.prevKey;
    }
    return steps.reverse();
  };

  for (let head = 0; head < queue.length; head++) {
    const key = queue[head];
    const state = states.get(key)!;
    const vs = viewsOf(state, newProto, oldProto);

    // 1) 意外接收：队首消息在当前状态没有任何接收迁移可消费
    for (const party of ["A", "B"] as const) {
      const v = vs[party];
      const headMsg = v.incoming[0];
      if (headMsg === undefined) continue;
      const consumable = (v.proto.outgoing.get(v.local) ?? []).some(
        (t) => t.action.kind === "recv" && t.action.message === headMsg
      );
      if (!consumable) {
        return { kind: "unexpected-reception", party, message: headMsg, state, trace: buildTrace(key) };
      }
    }

    // 2) 正常终止：双方都在终止状态且两条队列均为空 —— 本次会话结束，不再扩展
    const aFinal = newProto.protocol.finals.includes(state.a);
    const bFinal = oldProto.protocol.finals.includes(state.b);
    if (aFinal && bFinal && state.qAB.length === 0 && state.qBA.length === 0) {
      terminalKeys.push(key);
      continue;
    }

    // 3) 生成后继
    const succKeys: string[] = [];
    for (const party of ["A", "B"] as const) {
      const v = vs[party];
      for (const t of v.proto.outgoing.get(v.local) ?? []) {
        if (!isEnabled(v, t, capacity)) continue;
        const next = applyTransition(state, party, t);
        const nk = keyOf(next);
        const step: Step = { party, action: t.action, from: state, to: next };
        succKeys.push(nk);
        edgeStep.set(`${key}->${nk}`, step);
        if (!states.has(nk)) {
          states.set(nk, next);
          parent.set(nk, { prevKey: key, step });
          depth.set(nk, depth.get(key)! + 1);
          queue.push(nk);
        }
      }
    }
    edges.set(key, succKeys);

    // 4) 死锁：没有任何可执行动作，且不是正常终止
    if (succKeys.length === 0) {
      return { kind: "deadlock", info: classifyDeadlock(state, newProto, oldProto), state, trace: buildTrace(key) };
    }
  }

  // 5) 全部会话可终止性：每个可达全局状态都必须能到达某个正常终止状态
  const good = new Set<string>(terminalKeys);
  const reverse = new Map<string, string[]>();
  for (const [from, tos] of edges) {
    for (const to of tos) {
      const list = reverse.get(to) ?? [];
      list.push(from);
      reverse.set(to, list);
    }
  }
  const stack = [...terminalKeys];
  while (stack.length > 0) {
    const k = stack.pop()!;
    for (const p of reverse.get(k) ?? []) {
      if (!good.has(p)) {
        good.add(p);
        stack.push(p);
      }
    }
  }
  const bad = [...states.keys()].filter((k) => !good.has(k));
  if (bad.length === 0) {
    return { kind: "compatible", explored: states.size, terminalCount: terminalKeys.length };
  }

  const badSet = new Set(bad);
  let worst = bad[0];
  for (const k of bad) {
    if (depth.get(k)! < depth.get(worst)!) worst = k;
  }
  return {
    kind: "non-terminating",
    state: states.get(worst)!,
    trace: buildTrace(worst),
    cycle: findCycle(worst, badSet, edges, edgeStep),
  };
}

/** 在“无法到达正常终止”的坏区域内找一条环，作为活锁证据 */
function findCycle(
  start: string,
  bad: ReadonlySet<string>,
  edges: ReadonlyMap<string, string[]>,
  edgeStep: ReadonlyMap<string, Step>
): Step[] | null {
  const mark = new Map<string, "visiting" | "done">();
  const path: string[] = [];
  const pathSteps: Step[] = [];

  const dfs = (k: string): Step[] | null => {
    mark.set(k, "visiting");
    path.push(k);
    for (const nk of edges.get(k) ?? []) {
      if (!bad.has(nk)) continue;
      const step = edgeStep.get(`${k}->${nk}`)!;
      const m = mark.get(nk);
      if (m === "visiting") {
        const idx = path.indexOf(nk);
        return [...pathSteps.slice(idx), step];
      }
      if (m === undefined) {
        pathSteps.push(step);
        const found = dfs(nk);
        if (found) return found;
        pathSteps.pop();
      }
    }
    path.pop();
    mark.set(k, "done");
    return null;
  };

  return dfs(start);
}
