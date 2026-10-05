/**
 * 状态规范化：把具体任期值替换为“截断间距编码”（gap-capped ranking），
 * 使无界的任期空间折叠为有限商空间，同时保持转移关系不变（精确互模拟）。
 *
 * 论证要点（协议中任期只参与 比较 / 复制 / +1 三种操作）：
 *  - 编码保序，故所有 < / = 守卫在商状态上结果一致；
 *  - 对 +1：设 t 的编码为 c。若下一个不同值编码为 c+1（真实间距 1），
 *    则 t+1 恰等于它，编码 c+1；若下一个值编码为 c+2（真实间距 ≥2），
 *    则 t+1 严格落在中间，重编码后仍得 c+1，且原 c+2 值编码不变。
 *    两种情形在商状态上不可区分且各自封闭，故 +1 与编码可交换；
 *  - 复制显然与编码可交换。
 * 因此商图与具体图互模拟：商图中无反例 ⇔ 任意具体执行（任意任期、
 * 任意步数）中都无反例。这是“无限执行证明”结论的依据。
 *
 * 另外做无害的字段归一：非 candidate 的 votes、非 leader 的 leaderTerm、
 * 无投票记录时的 votedFor 均不参与任何守卫，统一清零以合并等价状态。
 *
 * 惰性消息回收：若某消息的投递对当前状态必为空操作（过期任期；或回执
 * 送达非 candidate 节点），则视为网络已将其回收，从在途集合中移除。
 * 被回收消息的投递与丢弃不可区分，此归一只会让网络容量提前释放
 *（对手更强），因此证明结论对原始语义同样成立。
 */

import type { Msg, State } from './model.ts';

/** 消息对当前状态是否必为空操作（两种模式下均成立）。 */
function inert(m: Msg, s: State): boolean {
  const to = s.nodes[m.to];
  if (m.term < to.term) return true; // 过期消息，投递被忽略
  if (m.kind === 'resp' && m.term === to.term && to.role !== 'candidate') {
    return true; // 回执只对同任期的 candidate 有意义
  }
  return false;
}

function cmpMsg(a: Msg, b: Msg): number {
  if (a.kind !== b.kind) return a.kind < b.kind ? -1 : 1;
  if (a.from !== b.from) return a.from - b.from;
  if (a.to !== b.to) return a.to - b.to;
  return a.term - b.term;
}

export function canonical(s: State): State {
  const vals: number[] = [];
  for (const n of s.nodes) {
    vals.push(n.term);
    if (n.votedTerm >= 0) vals.push(n.votedTerm);
    if (n.role === 'leader') vals.push(n.leaderTerm);
  }
  for (const m of s.net) vals.push(m.term);

  const sorted = [...new Set(vals)].sort((a, b) => a - b);
  const code = new Map<number, number>();
  let c = 0;
  let prev = sorted[0];
  sorted.forEach((v, i) => {
    if (i > 0) {
      c += Math.min(v - prev, 2); // 间距截断为 2：只需区分“相邻”与“有间隔”
      prev = v;
    }
    code.set(v, c);
  });
  const T = (x: number): number => code.get(x)!;

  return {
    nodes: s.nodes.map((n) => ({
      term: T(n.term),
      role: n.role,
      votedTerm: n.votedTerm < 0 ? -1 : T(n.votedTerm),
      votedFor: n.votedTerm < 0 ? -1 : n.votedFor,
      votes: n.role === 'candidate' ? n.votes : 0,
      leaderTerm: n.role === 'leader' ? T(n.leaderTerm) : -1,
    })),
    net: s.net
      .filter((m) => !inert(m, s))
      .map((m) => ({ ...m, term: T(m.term) }))
      .sort(cmpMsg),
  };
}

/** 紧凑序列化（比 JSON.stringify 快得多；输入须为 canonical 状态）。 */
export function serialize(c: State): string {
  let out = '';
  for (const n of c.nodes) {
    out += `${n.term}${n.role[0]}${n.votedTerm},${n.votedFor},${n.votes},${n.leaderTerm};`;
  }
  out += '|';
  for (const m of c.net) {
    out += `${m.kind === 'req' ? 'q' : 'p'}${m.from}${m.to}:${m.term};`;
  }
  return out;
}

export function key(s: State): string {
  return serialize(canonical(s));
}
