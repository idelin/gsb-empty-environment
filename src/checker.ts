/**
 * 兼容性检查器：把新版(A)与旧版对端(B)两台 CFSM 通过两条容量受限的
 * FIFO 队列组合成全局状态空间，用 BFS 探索，保证报告的问题轨迹最短。
 */
import { type Machine, type Side, receivableMessages, transitionsFrom } from './model.ts';

export interface GlobalState {
  a: string;
  b: string;
  /** A→B 方向队列（队首在下标 0） */
  ab: string[];
  /** B→A 方向队列 */
  ba: string[];
}

export interface Move {
  side: Side;
  kind: 'send' | 'recv' | 'tau';
  msg: string;
  from: string;
  to: string;
}

export interface Step {
  move: Move;
  /** 该步之后的全局状态 */
  after: GlobalState;
}

export type DeadlockKind = 'both-waiting' | 'buffer-full' | 'stuck';

export type Issue =
  | { type: 'unspecified-reception'; side: Side; localState: string; msg: string; expected: string[] }
  | { type: 'deadlock'; kind: DeadlockKind; aBlocker: string; bBlocker: string }
  | { type: 'livelock'; detail: string };

export interface CheckResult {
  ok: boolean;
  issue?: Issue;
  /** 到达问题状态（或示例正常会话）的最短动作序列 */
  trace: Step[];
  initial: GlobalState;
  finalState: GlobalState;
  /** 兼容时：一条最短正常终止会话轨迹 */
  sampleSession?: Step[];
  stats: { states: number; transitions: number; terminals: number };
}

interface Options {
  capacity: number;
}

const keyOf = (s: GlobalState): string => JSON.stringify(s);

export function checkCompat(newSide: Machine, oldPeer: Machine, opt: Options): CheckResult {
  const cap = opt.capacity;
  if (!Number.isInteger(cap) || cap < 1) throw new Error('队列容量必须是 >= 1 的整数');

  const initial: GlobalState = { a: newSide.initial, b: oldPeer.initial, ab: [], ba: [] };
  const startKey = keyOf(initial);

  const stateOf = new Map<string, GlobalState>([[startKey, initial]]);
  const parent = new Map<string, { prev: string; move: Move }>();
  const depth = new Map<string, number>([[startKey, 0]]);
  const succ = new Map<string, string[]>();
  const terminalKeys: string[] = [];

  const queue: string[] = [startKey];
  let transitions = 0;

  const buildTrace = (endKey: string): Step[] => {
    const steps: Step[] = [];
    let k = endKey;
    while (k !== startKey) {
      const p = parent.get(k)!;
      steps.push({ move: p.move, after: stateOf.get(k)! });
      k = p.prev;
    }
    return steps.reverse();
  };

  const finish = (issue: Issue, atKey: string): CheckResult => ({
    ok: false,
    issue,
    trace: buildTrace(atKey),
    initial,
    finalState: stateOf.get(atKey)!,
    stats: { states: stateOf.size, transitions, terminals: terminalKeys.length },
  });

  // 计算某侧在当前全局状态下的可用动作
  const movesFor = (s: GlobalState, side: Side): { move: Move; next: GlobalState }[] => {
    const me = side === 'A' ? newSide : oldPeer;
    const local = side === 'A' ? s.a : s.b;
    const inbox = side === 'A' ? s.ba : s.ab;
    const outbox = side === 'A' ? s.ab : s.ba;
    const out: { move: Move; next: GlobalState }[] = [];

    for (const t of transitionsFrom(me, local)) {
      if (t.kind === 'tau') {
        out.push({ move: { side, kind: 'tau', msg: '', from: t.from, to: t.to }, next: localMove(s, side, t.to) });
      } else if (t.kind === 'send') {
        if (outbox.length < cap) {
          const next = localMove(s, side, t.to);
          if (side === 'A') next.ab = [...s.ab, t.msg]; else next.ba = [...s.ba, t.msg];
          out.push({ move: { side, kind: 'send', msg: t.msg, from: t.from, to: t.to }, next });
        }
      } else {
        if (inbox.length > 0 && inbox[0] === t.msg) {
          const next = localMove(s, side, t.to);
          if (side === 'A') next.ba = s.ba.slice(1); else next.ab = s.ab.slice(1);
          out.push({ move: { side, kind: 'recv', msg: t.msg, from: t.from, to: t.to }, next });
        }
      }
    }
    return out;
  };

  const localMove = (s: GlobalState, side: Side, to: string): GlobalState =>
    side === 'A' ? { a: to, b: s.b, ab: s.ab, ba: s.ba } : { a: s.a, b: to, ab: s.ab, ba: s.ba };

  const isTerminal = (s: GlobalState): boolean =>
    newSide.finals.has(s.a) && oldPeer.finals.has(s.b) && s.ab.length === 0 && s.ba.length === 0;

  // 死锁分类：双方都在等待 / 缓冲区满 / 其它卡死
  const classify = (s: GlobalState): { kind: DeadlockKind; aBlocker: string; bBlocker: string } => {
    const blocker = (side: Side): { waited: boolean; full: boolean; desc: string } => {
      const me = side === 'A' ? newSide : oldPeer;
      const local = side === 'A' ? s.a : s.b;
      const inbox = side === 'A' ? s.ba : s.ab;
      const outbox = side === 'A' ? s.ab : s.ba;
      const trs = transitionsFrom(me, local);
      const hasRecv = trs.some((t) => t.kind === 'recv');
      const hasSend = trs.some((t) => t.kind === 'send');
      const waited = hasRecv && inbox.length === 0;
      const full = hasSend && outbox.length >= cap;
      let desc: string;
      if (trs.length === 0) {
        desc = me.finals.has(local)
          ? `已到达终止状态 ${local}，但对端仍未完成`
          : `停在非终止状态 ${local}（无任何出边）`;
      }
      else if (waited && full) desc = `在 ${local} 等待接收，同时发送被满队列阻塞`;
      else if (waited) desc = `在 ${local} 等待接收 [${[...receivableMessages(me, local)].join(', ')}]，但对方队列为空`;
      else if (full) desc = `在 ${local} 等待发送，但容量为 ${cap} 的队列已满`;
      else desc = `在 ${local} 无法继续`;
      return { waited, full, desc };
    };
    const ra = blocker('A');
    const rb = blocker('B');
    const kind: DeadlockKind =
      ra.waited && rb.waited && !ra.full && !rb.full ? 'both-waiting'
      : ra.full || rb.full ? 'buffer-full'
      : 'stuck';
    return { kind, aBlocker: ra.desc, bBlocker: rb.desc };
  };

  // BFS 主循环
  for (let head = 0; head < queue.length; head++) {
    const k = queue[head];
    const s = stateOf.get(k)!;

    // 1) 意外接收：队首消息在本地 τ 闭包内不可接收
    for (const side of ['A', 'B'] as Side[]) {
      const me = side === 'A' ? newSide : oldPeer;
      const local = side === 'A' ? s.a : s.b;
      const inbox = side === 'A' ? s.ba : s.ab;
      if (inbox.length > 0) {
        const acceptable = receivableMessages(me, local);
        if (!acceptable.has(inbox[0])) {
          return finish(
            { type: 'unspecified-reception', side, localState: local, msg: inbox[0], expected: [...acceptable] },
            k,
          );
        }
      }
    }

    // 2) 展开后继
    const moves = [...movesFor(s, 'A'), ...movesFor(s, 'B')];
    const nextKeys: string[] = [];
    for (const { move, next } of moves) {
      transitions++;
      const nk = keyOf(next);
      nextKeys.push(nk);
      if (!stateOf.has(nk)) {
        stateOf.set(nk, next);
        parent.set(nk, { prev: k, move });
        depth.set(nk, depth.get(k)! + 1);
        queue.push(nk);
      }
    }
    succ.set(k, nextKeys);

    // 3) 无后继：正常终止 or 死锁
    if (moves.length === 0) {
      if (isTerminal(s)) {
        terminalKeys.push(k);
      } else {
        const c = classify(s);
        return finish({ type: 'deadlock', kind: c.kind, aBlocker: c.aBlocker, bBlocker: c.bBlocker }, k);
      }
    }
  }

  // 4) 活锁检查：是否存在无法到达正常终止的全局状态
  const coReach = new Set<string>(terminalKeys);
  {
    const preds = new Map<string, string[]>();
    for (const [from, tos] of succ) for (const to of tos) {
      if (!preds.has(to)) preds.set(to, []);
      preds.get(to)!.push(from);
    }
    const stack = [...terminalKeys];
    while (stack.length > 0) {
      const k = stack.pop()!;
      for (const p of preds.get(k) ?? []) {
        if (!coReach.has(p)) { coReach.add(p); stack.push(p); }
      }
    }
  }

  let worstKey: string | null = null;
  for (const k of stateOf.keys()) {
    if (!coReach.has(k)) {
      if (worstKey === null || depth.get(k)! < depth.get(worstKey)!) worstKey = k;
    }
  }
  if (worstKey !== null) {
    const detail = terminalKeys.length === 0
      ? '不存在任何可达的正常终止状态，会话无法完成'
      : '从该状态出发已无法到达任何正常终止状态（会话可能无限循环）';
    return finish({ type: 'livelock', detail }, worstKey);
  }

  // 5) 全部通过
  const sample = terminalKeys.length > 0 ? buildTrace(terminalKeys[0]) : undefined;
  return {
    ok: true,
    trace: [],
    initial,
    finalState: initial,
    sampleSession: sample,
    stats: { states: stateOf.size, transitions, terminals: terminalKeys.length },
  };
}
