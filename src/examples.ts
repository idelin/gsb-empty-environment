/** 内置演示场景：一个旧版服务器协议 + 若干个新版客户端变体 */

import { indexProtocol, IndexedProtocol, parseProtocol, ProtocolJson } from "./protocol";

export interface DemoScenario {
  readonly id: string;
  readonly title: string;
  readonly description: string;
  readonly newProtocol: IndexedProtocol;
  readonly oldProtocol: IndexedProtocol;
}

/** 旧版服务器：login → welcome → 多轮 query/result → bye */
const oldServerV1: ProtocolJson = {
  name: "old",
  initial: "Idle",
  finals: ["Done"],
  transitions: [
    { from: "Idle", label: "?login", to: "Ready" },
    { from: "Ready", label: "!welcome", to: "Serving" },
    { from: "Serving", label: "?query", to: "Replying" },
    { from: "Replying", label: "!result", to: "Serving" },
    { from: "Serving", label: "?bye", to: "Done" },
  ],
};

/** 新版客户端（兼容）：含内部动作 tau，可多次查询后告别 */
const newClientV2: ProtocolJson = {
  name: "new",
  initial: "C0",
  finals: ["C4"],
  transitions: [
    { from: "C0", label: "!login", to: "C1" },
    { from: "C1", label: "?welcome", to: "C2" },
    { from: "C2", label: "tau", to: "C2p" },
    { from: "C2p", label: "!query", to: "C3" },
    { from: "C3", label: "?result", to: "C2p" },
    { from: "C2p", label: "!bye", to: "C4" },
  ],
};

/** 新版变体：上来先等旧版主动说 hello —— 旧版也在等 login，双方互相等待 */
const newClientV2Waiting: ProtocolJson = {
  name: "new",
  initial: "C0",
  finals: ["C4"],
  transitions: [
    { from: "C0", label: "?hello", to: "C1" },
    { from: "C1", label: "!login", to: "C2" },
    { from: "C2", label: "?welcome", to: "C3" },
    { from: "C3", label: "!bye", to: "C4" },
  ],
};

/** 新版变体：第一句就发 bye —— 旧版在 Idle 只认 login，构成意外接收 */
const newClientV2Rude: ProtocolJson = {
  name: "new",
  initial: "C0",
  finals: ["C2"],
  transitions: [
    { from: "C0", label: "!bye", to: "C1" },
    { from: "C1", label: "?welcome", to: "C2" },
  ],
};

/** 新版变体：登录后陷入无限查询/内部循环，永远不说 bye —— 活锁 */
const newClientV2Loop: ProtocolJson = {
  name: "new",
  initial: "C0",
  finals: [],
  transitions: [
    { from: "C0", label: "!login", to: "C1" },
    { from: "C1", label: "?welcome", to: "C2" },
    { from: "C2", label: "tau", to: "C2" },
    { from: "C2", label: "!query", to: "C3" },
    { from: "C3", label: "?result", to: "C2" },
  ],
};

const build = (json: ProtocolJson): IndexedProtocol => indexProtocol(parseProtocol(json));
const old = build(oldServerV1);

export const demoScenarios: readonly DemoScenario[] = [
  {
    id: "compatible",
    title: "场景 1：兼容的新版客户端",
    description: "新版含内部动作 tau 与多轮查询，应与旧版服务器正常完成全部会话。",
    newProtocol: build(newClientV2),
    oldProtocol: old,
  },
  {
    id: "both-waiting",
    title: "场景 2：双方都在等待的死锁",
    description: "新版先等旧版的 hello，而旧版也在等新版的 login —— 双向等待。",
    newProtocol: build(newClientV2Waiting),
    oldProtocol: old,
  },
  {
    id: "unexpected-reception",
    title: "场景 3：意外接收",
    description: "新版第一句就发 bye，旧版在 Idle 状态只期望 login。",
    newProtocol: build(newClientV2Rude),
    oldProtocol: old,
  },
  {
    id: "non-terminating",
    title: "场景 4：活锁（永远无法正常终止）",
    description: "新版登录后在查询循环里无限打转，永远不说 bye。",
    newProtocol: build(newClientV2Loop),
    oldProtocol: old,
  },
];
