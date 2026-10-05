/**
 * 三节点投票选主协议模型（类 Raft 的投票子协议）。
 *
 * 每张票携带任期；每节点每任期最多投一票（--buggy 模式下故意违反此约束，
 * 用于演示反例输出）。网络可任意延迟、重排、丢弃消息。
 * 此外网络会立即回收“投递必为空操作”的惰性消息（见 canon.ts），
 * 容量上限 netCap 按仍在途的有效消息计。
 *
 * 转移规则中任期只参与三种操作：比较（< / =）、复制、+1。
 * 该性质是 canon.ts 中“精确互模拟”论证的前提，修改协议时请保持。
 */

export const NODE_IDS = [0, 1, 2] as const;
export const MAJORITY = 2; // 3 节点集群的多数派

export type Role = 'follower' | 'candidate' | 'leader';

export interface NodeState {
  term: number;       // 当前任期
  role: Role;
  votedTerm: number;  // 本节点投过票的任期；-1 表示当前任期内未投票记录
  votedFor: number;   // 投给了谁；-1 表示无
  votes: number;      // 已收选票的位掩码，仅 candidate 角色下有意义
  leaderTerm: number; // 当选时所在任期，仅 leader 角色下有意义
}

export interface Msg {
  kind: 'req' | 'resp'; // req = 请求投票，resp = 授予选票
  from: number;
  to: number;
  term: number;
}

export interface State {
  nodes: NodeState[];
  net: Msg[]; // 在途消息多重集合（无序，天然覆盖延迟与重排）
}

export interface Options {
  netCap: number;  // 在途消息数上限（有界枚举的网络界）
  buggy: boolean;  // true 时允许同任期重复投票（注入故障，用于演示反例）
}

export interface Step {
  label: string;
  state: State;
}

export function initialState(): State {
  return {
    nodes: NODE_IDS.map(() => ({
      term: 0,
      role: 'follower' as Role,
      votedTerm: -1,
      votedFor: -1,
      votes: 0,
      leaderTerm: -1,
    })),
    net: [],
  };
}

function cloneNodes(nodes: NodeState[]): NodeState[] {
  return nodes.map((n) => ({ ...n }));
}

function removeAt(net: Msg[], idx: number): Msg[] {
  return net.filter((_, i) => i !== idx);
}

function popcount(x: number): number {
  let c = 0;
  while (x) {
    x &= x - 1;
    c++;
  }
  return c;
}

export function msgStr(m: Msg): string {
  return m.kind === 'req'
    ? `请求投票(n${m.from}→n${m.to}, 任期${m.term})`
    : `授予选票(n${m.from}→n${m.to}, 任期${m.term})`;
}

/** 枚举状态 s 的所有后继（确定性动作集合，非确定性体现在多条出边上）。 */
export function successors(s: State, opts: Options): Step[] {
  const out: Step[] = [];

  // 动作 1：任一节点超时，任期 +1，转为候选人并自投一票，向其余节点拉票。
  // 网络满时新消息直接丢失（与“发出后被丢弃”等价，drop 动作已覆盖）。
  for (const i of NODE_IDS) {
    const t = s.nodes[i].term + 1;
    const nodes = cloneNodes(s.nodes);
    nodes[i] = {
      term: t,
      role: 'candidate',
      votedTerm: t,
      votedFor: i,
      votes: 1 << i,
      leaderTerm: -1,
    };
    const net = [...s.net];
    for (const j of NODE_IDS) {
      if (j !== i && net.length < opts.netCap) {
        net.push({ kind: 'req', from: i, to: j, term: t });
      }
    }
    out.push({ label: `n${i} 超时，发起任期 ${t} 竞选（自投一票）`, state: { nodes, net } });
  }

  // 动作 2/3：网络中的每条消息可被丢弃或投递。
  s.net.forEach((m, idx) => {
    out.push({
      label: `网络丢弃 ${msgStr(m)}`,
      state: { nodes: cloneNodes(s.nodes), net: removeAt(s.net, idx) },
    });
    out.push({ label: `投递 ${msgStr(m)}`, state: deliver(s, m, idx, opts) });
  });

  return out;
}

function deliver(s: State, m: Msg, idx: number, opts: Options): State {
  const nodes = cloneNodes(s.nodes);
  const net = removeAt(s.net, idx);
  const n = nodes[m.to];

  if (m.kind === 'req') {
    if (m.term < n.term) return { nodes, net }; // 过期请求，忽略
    if (m.term > n.term) {
      // 发现更高任期：让位并清空投票记录
      nodes[m.to] = {
        term: m.term,
        role: 'follower',
        votedTerm: -1,
        votedFor: -1,
        votes: 0,
        leaderTerm: -1,
      };
    }
    const target = nodes[m.to];
    const alreadyVotedThisTerm = target.votedTerm === m.term;
    if (!alreadyVotedThisTerm || opts.buggy) {
      target.votedTerm = m.term;
      target.votedFor = m.from;
      if (net.length < opts.netCap) {
        net.push({ kind: 'resp', from: m.to, to: m.from, term: m.term });
      }
    }
    return { nodes, net };
  }

  // resp：授予选票的回执
  if (m.term > n.term) {
    nodes[m.to] = {
      term: m.term,
      role: 'follower',
      votedTerm: -1,
      votedFor: -1,
      votes: 0,
      leaderTerm: -1,
    };
    return { nodes, net };
  }
  if (m.term < n.term || n.role !== 'candidate') return { nodes, net };
  n.votes |= 1 << m.from;
  if (popcount(n.votes) >= MAJORITY) {
    n.role = 'leader';
    n.leaderTerm = n.term;
  }
  return { nodes, net };
}

/** 安全性质违反：同一任期出现两个多数派领导者。返回描述或 null。 */
export function violation(s: State): string | null {
  for (const i of NODE_IDS) {
    for (const j of NODE_IDS) {
      if (j <= i) continue;
      const a = s.nodes[i];
      const b = s.nodes[j];
      if (a.role === 'leader' && b.role === 'leader' && a.leaderTerm === b.leaderTerm) {
        return `n${i} 与 n${j} 同为任期 ${a.leaderTerm} 的多数派领导者`;
      }
    }
  }
  return null;
}
