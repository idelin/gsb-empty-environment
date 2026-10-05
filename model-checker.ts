/**
 * 三节点投票选主协议 —— 显式状态模型检查器
 *
 * 协议（类 Raft 多数派投票）：
 *   - 每张票都带任期（term）；每个节点在每个任期最多投一票。
 *   - 节点可发起竞选（term+1、自投一票、广播 RequestVote）。
 *   - 收到更高任期的 RequestVote 时先步进任期；若该任期尚未投票则授予选票。
 *   - 候选人收齐多数派（>N/2）选票后成为该任期的领导者。
 *
 * 网络模型：消息可被任意延迟（乱序投递）和丢弃，网络容量有界。
 *
 * 检查的安全性质：同一任期不可能出现两个获得多数票的领导者。
 *
 * 结果三态：
 *   - counterexample : 找到反例（BFS 保证最短），输出完整轨迹；
 *   - proof          : 状态空间被穷尽（有界建模下状态有限），任意执行
 *                      （包括无限执行）都不可能违反性质 —— 构成证明；
 *   - bounded        : 因深度/状态数上限被截断，"未发现反例"，不是证明。
 */

// ---------------------------------------------------------------- 模型定义

type NodeId = number;

interface ReqMsg {
  kind: "req";
  term: number;
  from: NodeId;
  to: NodeId;
}

interface VoteMsg {
  kind: "vote"; // 只建模“授予”的票；拒绝票等价于丢弃，不占用网络
  term: number;
  from: NodeId;
  to: NodeId;
}

type Msg = ReqMsg | VoteMsg;

const NONE = -1;

interface NodeState {
  term: number;             // 当前任期
  votedFor: NodeId[];       // votedFor[t] = 在任期 t 投给的节点，NONE 表示未投
  votesFrom: boolean[];     // 当前任期竞选中，已给我投票的节点集合
  leader: boolean[];        // leader[t] = 我已在任期 t 成为领导者（历史记录）
}

interface State {
  nodes: NodeState[];
  net: Msg[]; // 在途消息多重集合（序列化时排序，保证 canonical）
}

interface Config {
  numNodes: number;
  maxTerm: number;   // 任期上界（有界枚举）
  netCap: number;    // 网络容量上界（有界枚举）；发送时若已满则消息直接丢失
  maxDepth: number;  // BFS 深度上界；Infinity 表示不截断
  maxStates: number; // 状态数上限（保险丝）
  buggy: boolean;    // true 时注入故障：节点在同一任期可重复投票
}

function initialState(cfg: Config): State {
  const nodes: NodeState[] = [];
  for (let i = 0; i < cfg.numNodes; i++) {
    nodes.push({
      term: 0,
      votedFor: new Array(cfg.maxTerm + 1).fill(NONE),
      votesFrom: new Array(cfg.numNodes).fill(false),
      leader: new Array(cfg.maxTerm + 1).fill(false),
    });
  }
  return { nodes, net: [] };
}

function serialize(s: State): string {
  const net = s.net
    .map((m) => `${m.kind}:${m.term}:${m.from}:${m.to}`)
    .sort();
  const nodes = s.nodes.map(
    (n) =>
      `${n.term}|${n.votedFor.join(",")}|${n.votesFrom
        .map((b) => (b ? 1 : 0))
        .join("")}|${n.leader.map((b) => (b ? 1 : 0)).join("")}`
  );
  return nodes.join(";") + "#" + net.join(",");
}

function majority(cfg: Config): number {
  return Math.floor(cfg.numNodes / 2) + 1;
}

/** 安全性质违反：存在某个任期有两个及以上领导者。返回该任期，否则 -1。 */
function findViolation(s: State, cfg: Config): number {
  for (let t = 1; t <= cfg.maxTerm; t++) {
    let leaders = 0;
    for (const n of s.nodes) if (n.leader[t]) leaders++;
    if (leaders >= 2) return t;
  }
  return -1;
}

// ---------------------------------------------------------------- 后继枚举

interface Succ {
  action: string;
  state: State;
  violation: number; // -1 表示未违反
}

function successors(s: State, cfg: Config): Succ[] {
  const out: Succ[] = [];
  const N = cfg.numNodes;

  // 1) 竞选：term+1，自投一票，广播 RequestVote（网络满则消息丢失）
  for (let i = 0; i < N; i++) {
    if (s.nodes[i].term >= cfg.maxTerm) continue;
    const ns = structuredClone(s);
    const n = ns.nodes[i];
    n.term++;
    n.votedFor[n.term] = i;
    n.votesFrom = new Array(N).fill(false);
    n.votesFrom[i] = true;
    for (let j = 0; j < N; j++) {
      if (j === i) continue;
      if (ns.net.length < cfg.netCap)
        ns.net.push({ kind: "req", term: n.term, from: i, to: j });
    }
    out.push({
      action: `N${i} 发起竞选 (term=${n.term})，自投并广播 RequestVote`,
      state: ns,
      violation: findViolation(ns, cfg),
    });
  }

  // 2) 投递网络中的第 k 条消息（任意乱序 = 延迟建模）
  for (let k = 0; k < s.net.length; k++) {
    const ns = structuredClone(s);
    const [m] = ns.net.splice(k, 1);
    const n = ns.nodes[m.to];
    let desc = "";

    if (m.kind === "req") {
      if (m.term > n.term) {
        n.term = m.term; // 步进到更高任期，不再是旧任期的候选人
        n.votesFrom = new Array(N).fill(false);
      }
      if (m.term === n.term) {
        const prev = n.votedFor[m.term];
        const canVote = prev === NONE || prev === m.from || cfg.buggy;
        if (canVote) {
          n.votedFor[m.term] = m.from;
          if (ns.net.length < cfg.netCap)
            ns.net.push({ kind: "vote", term: m.term, from: m.to, to: m.from });
          desc =
            prev !== NONE && prev !== m.from
              ? `N${m.to} 在 term=${m.term} 已投给 N${prev}，仍（错误地）改投 N${m.from}`
              : `N${m.to} 在 term=${m.term} 投票给 N${m.from}`;
        } else {
          desc = `N${m.to} 拒绝 N${m.from} 的投票请求 (term=${m.term}，已投给 N${prev})`;
        }
      } else {
        desc = `N${m.to} 忽略过期投票请求 (term=${m.term} < 当前 term=${n.term})`;
      }
    } else {
      // 授予票：仅当仍是该任期候选人且该投票者尚未计入时有效
      if (
        m.term === n.term &&
        n.votedFor[m.term] === m.to &&
        !n.votesFrom[m.from]
      ) {
        n.votesFrom[m.from] = true;
        const count = n.votesFrom.filter(Boolean).length;
        desc = `N${m.to} 收到 N${m.from} 的选票 (term=${m.term})，得票 ${count}/${N}`;
        if (count >= majority(cfg) && !n.leader[m.term]) {
          n.leader[m.term] = true;
          desc += ` —— 获得多数，成为 term=${m.term} 的领导者`;
        }
      } else {
        desc = `N${m.to} 忽略过期/重复选票 (term=${m.term})`;
      }
    }
    out.push({
      action: `投递 [${m.kind} term=${m.term} N${m.from}→N${m.to}]：${desc}`,
      state: ns,
      violation: findViolation(ns, cfg),
    });
  }

  // 3) 丢弃网络中的第 k 条消息
  for (let k = 0; k < s.net.length; k++) {
    const ns = structuredClone(s);
    const [m] = ns.net.splice(k, 1);
    out.push({
      action: `丢弃 [${m.kind} term=${m.term} N${m.from}→N${m.to}]`,
      state: ns,
      violation: -1,
    });
  }

  return out;
}

// ---------------------------------------------------------------- BFS 枚举

type Outcome =
  | { kind: "counterexample"; trace: { action: string; state: State }[]; explored: number }
  | { kind: "proof"; explored: number }
  | { kind: "bounded"; explored: number; reason: string };

interface Rec {
  depth: number;
  parent: string | null;
  action: string | null;
  state: State;
}

function modelCheck(cfg: Config): Outcome {
  const init = initialState(cfg);
  const seen = new Map<string, Rec>();
  seen.set(serialize(init), { depth: 0, parent: null, action: null, state: init });
  const queue: string[] = [serialize(init)];
  let head = 0;

  while (head < queue.length) {
    const key = queue[head++];
    const rec = seen.get(key)!;
    if (seen.size > cfg.maxStates)
      return { kind: "bounded", explored: seen.size, reason: `超过状态数上限 ${cfg.maxStates}` };
    if (rec.depth >= cfg.maxDepth) continue;

    for (const succ of successors(rec.state, cfg)) {
      if (succ.violation >= 0) {
        // 回溯最短反例轨迹
        const trace: { action: string; state: State }[] = [
          { action: succ.action, state: succ.state },
        ];
        let cur: Rec = rec;
        while (cur.parent !== null) {
          trace.unshift({ action: cur.action!, state: cur.state });
          cur = seen.get(cur.parent)!;
        }
        return { kind: "counterexample", trace, explored: seen.size };
      }
      const sk = serialize(succ.state);
      if (!seen.has(sk)) {
        seen.set(sk, { depth: rec.depth + 1, parent: key, action: succ.action, state: succ.state });
        queue.push(sk);
      }
    }
  }

  if (cfg.maxDepth !== Infinity) {
    // 队列耗尽但仍可能因深度截断而未展开部分状态
    for (const rec of seen.values())
      if (rec.depth >= cfg.maxDepth)
        return { kind: "bounded", explored: seen.size, reason: `深度上限 ${cfg.maxDepth}` };
  }
  return { kind: "proof", explored: seen.size };
}

// ---------------------------------------------------------------- 输出

function fmtState(s: State, cfg: Config): string {
  const nodes = s.nodes
    .map((n, i) => {
      const leadTerms: number[] = [];
      n.leader.forEach((b, t) => b && leadTerms.push(t));
      const voted = n.votedFor[n.term] === NONE ? "-" : `N${n.votedFor[n.term]}`;
      const votes: number[] = [];
      n.votesFrom.forEach((b, j) => b && votes.push(j));
      return `N${i}{term=${n.term}, 投给=${voted}, 得票={${votes}}, 领导者任期=[${leadTerms}]}`;
    })
    .join(" ");
  const net =
    s.net.length === 0
      ? "空"
      : s.net.map((m) => `${m.kind}[t${m.term} N${m.from}→N${m.to}]`).join(" ");
  return `    ${nodes}\n    网络: ${net}`;
}

function report(name: string, cfg: Config, outcome: Outcome): void {
  console.log(`\n=== 场景: ${name} ===`);
  console.log(
    `配置: 节点=${cfg.numNodes}, 任期上限=${cfg.maxTerm}, 网络容量=${cfg.netCap}, ` +
      `深度上限=${cfg.maxDepth === Infinity ? "无" : cfg.maxDepth}, 故障注入=${cfg.buggy}`
  );
  switch (outcome.kind) {
    case "counterexample":
      console.log(`结果: ✗ 违反安全性质 —— 最短反例长度 ${outcome.trace.length} 步（已探索 ${outcome.explored} 个状态）`);
      outcome.trace.forEach((step, i) => {
        console.log(`  步骤 ${i + 1}: ${step.action}`);
        console.log(fmtState(step.state, cfg));
      });
      break;
    case "proof":
      console.log(
        `结果: ✓ 安全 —— 已穷尽全部 ${outcome.explored} 个可达状态。\n` +
          `  状态空间有限，任何执行（包括无限执行）的每个前缀都落在该集合内，\n` +
          `  因此这是对“同任期不会有两个多数派领导者”的完整证明（非仅“未发现”）。`
      );
      break;
    case "bounded":
      console.log(
        `结果: ? 未发现反例（已探索 ${outcome.explored} 个状态），但探索被截断（${outcome.reason}）。\n` +
          `  这不是证明：更深的调度仍可能违反性质。`
      );
      break;
  }
}

// ---------------------------------------------------------------- 入口

function main(): void {
  const args = process.argv.slice(2);
  const base: Config = { numNodes: 3, maxTerm: 2, netCap: 4, maxDepth: Infinity, maxStates: 1_000_000, buggy: false };

  if (args.includes("--buggy")) {
    report("故障注入：同任期可重复投票", { ...base, buggy: true }, modelCheck({ ...base, buggy: true }));
    return;
  }
  if (args.includes("--bounded")) {
    const d = Number(args[args.indexOf("--bounded") + 1] ?? 4);
    report(`有界探索 (depth=${d})`, { ...base, maxDepth: d }, modelCheck({ ...base, maxDepth: d }));
    return;
  }
  if (args.includes("--exhaustive")) {
    report("正确协议，穷尽探索", base, modelCheck(base));
    return;
  }

  // 默认：演示三种结果
  report("正确协议，穷尽探索（无限执行证明）", base, modelCheck(base));
  report("故障注入：同任期可重复投票", { ...base, buggy: true }, modelCheck({ ...base, buggy: true }));
  report("正确协议，深度截断 (depth=4)", { ...base, maxDepth: 4 }, modelCheck({ ...base, maxDepth: 4 }));
}

main();
