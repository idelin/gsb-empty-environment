import {
  applyConfigCommand,
  quorumReached,
  type ConfigCommand,
  type ConfigState,
  type NodeId,
} from "./config.ts";

/**
 * 日志条目。每条条目记录其追加时生效的配置（Raft 语义：
 * 配置在条目追加时即生效，提交判定按条目各自携带的配置执行）。
 */
export type LogEntry = {
  readonly term: number;
  readonly config: ConfigState;
} & (
  | { readonly type: "op"; readonly command: string }
  | { readonly type: "config"; readonly command: ConfigCommand }
);

export function sameEntry(a: LogEntry, b: LogEntry): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export class Node {
  readonly id: NodeId;
  readonly baseConfig: ConfigState;
  log: LogEntry[] = [];
  /** 已提交前缀长度（1 起算：log[0..commitIndex-1] 已提交）。 */
  commitIndex = 0;
  term: number;

  constructor(id: NodeId, baseConfig: ConfigState, term: number) {
    this.id = id;
    this.baseConfig = baseConfig;
    this.term = term;
  }

  /** 折叠前 count 条日志中的配置项，得到该前缀处的配置状态。 */
  configAt(count: number): ConfigState {
    let config = this.baseConfig;
    const limit = Math.min(count, this.log.length);
    for (let i = 0; i < limit; i += 1) {
      const entry = this.log[i];
      if (entry.type === "config") {
        config = applyConfigCommand(config, entry.command);
      }
    }
    return config;
  }

  /** 当前生效配置：随最新日志立即生效（不要求已提交）。 */
  get activeConfig(): ConfigState {
    return this.configAt(this.log.length);
  }

  /** 已提交配置：仅折叠已提交前缀中的配置项。 */
  get committedConfig(): ConfigState {
    return this.configAt(this.commitIndex);
  }

  hasEntry(index: number, entry: LogEntry): boolean {
    return (
      this.log.length >= index && sameEntry(this.log[index - 1], entry)
    );
  }
}

/**
 * 单领导者确定性模拟器。网络行为通过 replicateTo 显式指定
 * （未被送达的节点等价于被分区隔离）。
 */
export class Cluster {
  readonly nodes = new Map<NodeId, Node>();
  readonly initialConfig: ConfigState;
  leaderId: NodeId;
  term = 1;

  constructor(members: readonly NodeId[]) {
    if (members.length === 0) throw new Error("初始成员组不能为空");
    this.initialConfig = { kind: "stable", members: [...members] };
    for (const id of members) {
      this.nodes.set(id, new Node(id, this.initialConfig, this.term));
    }
    this.leaderId = members[0];
  }

  addNode(id: NodeId): Node {
    if (this.nodes.has(id)) throw new Error(`节点 ${id} 已存在`);
    const node = new Node(id, this.initialConfig, this.term);
    this.nodes.set(id, node);
    return node;
  }

  leader(): Node {
    const node = this.nodes.get(this.leaderId);
    if (!node) throw new Error(`领导者 ${this.leaderId} 不存在`);
    return node;
  }

  /** 普通日志提案：按领导者当前生效配置追加。 */
  propose(command: string): LogEntry {
    const leader = this.leader();
    const entry: LogEntry = {
      term: this.term,
      type: "op",
      command,
      config: leader.activeConfig,
    };
    leader.log.push(entry);
    return entry;
  }

  /**
   * 配置变更提案（联合共识路径）：
   * - 同一时刻只允许一个未提交的配置变更；
   * - 迁移必须合法（stable -> joint -> stable），跳步直接抛错；
   * - 禁止 switch-direct。
   *
   * 配置项本身也按所处阶段的规则提交：
   * - C_old,new 一经追加集群即进入联合期，因此它必须在新旧两组
   *   双多数下提交；
   * - C_new 是联合期的终点，联合期直到它提交才结束，因此它同样
   *   按联合规则（双多数）提交。
   */
  proposeConfig(command: ConfigCommand): LogEntry {
    if (command.kind === "switch-direct") {
      throw new Error(
        "非法提案：禁止跳过联合确认直接切换配置，请使用 enter-joint / leave-joint",
      );
    }
    const leader = this.leader();
    const pending = leader.log.some(
      (entry, i) => entry.type === "config" && i >= leader.commitIndex,
    );
    if (pending) {
      throw new Error("存在未提交的配置变更：必须先完成当前阶段");
    }
    const next = applyConfigCommand(leader.activeConfig, command); // 状态机校验
    const commitConfig =
      command.kind === "enter-joint" ? next : leader.activeConfig;
    const entry: LogEntry = {
      term: this.term,
      type: "config",
      command,
      config: commitConfig,
    };
    leader.log.push(entry);
    return entry;
  }

  /**
   * 反例专用：绕过联合共识，直接追加“跳到新配置”的配置项，
   * 并按旧配置（stable）规则提交 —— 模拟错误的直接切换实现。
   */
  unsafeForceDirectSwitch(newMembers: readonly NodeId[]): LogEntry {
    const leader = this.leader();
    const entry: LogEntry = {
      term: this.term,
      type: "config",
      command: { kind: "switch-direct", newMembers: [...newMembers] },
      config: leader.activeConfig,
    };
    leader.log.push(entry);
    return entry;
  }

  /** 把领导者日志与提交进度复制给指定节点（未列出者视为被分区）。 */
  replicateTo(ids: readonly NodeId[]): void {
    const leader = this.leader();
    for (const id of ids) {
      if (id === this.leaderId) continue;
      const node = this.nodes.get(id);
      if (!node) throw new Error(`未知节点 ${id}`);
      node.log = [...leader.log];
      node.term = this.term;
      node.commitIndex = Math.min(leader.commitIndex, node.log.length);
    }
  }

  /**
   * 提交判定：从日志尾部向前找到最高索引 i，使得
   * log[i] 是本任期条目，且其持有者集合在 log[i].config
   * 要求的每一个分组中都达到多数。提交 i 即提交整个前缀。
   */
  advanceCommitIndex(): number {
    const leader = this.leader();
    for (let i = leader.log.length; i > leader.commitIndex; i -= 1) {
      const entry = leader.log[i - 1];
      if (entry.term !== this.term) continue;
      const holders = new Set<NodeId>();
      for (const node of this.nodes.values()) {
        if (node.hasEntry(i, entry)) holders.add(node.id);
      }
      if (quorumReached(entry.config, holders)) {
        leader.commitIndex = i;
        break;
      }
    }
    return leader.commitIndex;
  }
}
