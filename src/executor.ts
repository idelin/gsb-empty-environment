/**
 * 依赖任务执行器（DAG executor）。
 *
 * 核心语义：
 * - 节点按依赖拓扑调度，全部前驱成功后才会启动。
 * - 节点失败或被撤销时，撤销沿依赖边向后传播：
 *   - 尚未启动（pending）的后继随之撤销，并记录原因链；
 *   - 已经启动（running）的后继不受影响，保留自身执行结果。
 * - 一个节点的多个前驱同时失败/被撤销时，全部记入 causes，
 *   并按节点编号（数字按数值、字符串按字典序）选出主因 primaryCause，
 *   原因链 chain 沿主因一路回溯到根因。
 */

export type NodeId = string | number;

export type NodeStatus =
  | 'pending'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'revoked';

export interface RevocationInfo {
  /** manual：显式 revoke；propagated：由上游失败/撤销传播而来 */
  kind: 'manual' | 'propagated';
  /** 直接原因：失败 / 被撤销 / 运行中被请求撤销的前驱节点 */
  causes: NodeId[];
  /** 主因：直接原因中节点编号最小者 */
  primaryCause: NodeId;
  /** 原因链：从根因节点沿主因路径到本节点 */
  chain: NodeId[];
  /** 可选说明（如手动撤销原因） */
  message?: string;
}

export interface NodeResult {
  id: NodeId;
  status: NodeStatus;
  /** 任务返回值（status === 'succeeded' 时存在） */
  value?: unknown;
  /** 任务抛出的错误（status === 'failed' 时存在） */
  error?: unknown;
  /** 撤销信息（status === 'revoked' 时存在） */
  revocation?: RevocationInfo;
  /** 运行期间收到过撤销请求（节点已启动，结果仍然保留） */
  revokeRequested: boolean;
}

export interface TaskContext {
  id: NodeId;
  /** 运行中被请求撤销时触发，任务可协作式退出 */
  signal: AbortSignal;
}

export type Task = (ctx: TaskContext) => unknown | Promise<unknown>;

export interface RunReport {
  results: ReadonlyMap<NodeId, NodeResult>;
  succeeded: NodeId[];
  failed: NodeId[];
  revoked: NodeId[];
}

export interface ExecutorOptions {
  /** 最大并发数，默认不限 */
  concurrency?: number;
}

/** 节点编号比较：数字按数值；纯数字字符串按数值；其余按字典序；数字排在非数字前。 */
export function compareNodeIds(a: NodeId, b: NodeId): number {
  if (a === b) return 0;
  const na = toNumber(a);
  const nb = toNumber(b);
  if (na !== null && nb !== null) return na - nb;
  if (na !== null) return -1;
  if (nb !== null) return 1;
  const sa = String(a);
  const sb = String(b);
  return sa < sb ? -1 : 1;
}

function toNumber(id: NodeId): number | null {
  if (typeof id === 'number') return id;
  if (/^-?\d+$/.test(id)) return Number(id);
  return null;
}

interface NodeRecord {
  id: NodeId;
  task: Task;
  deps: NodeId[];
  dependents: NodeId[];
  status: NodeStatus;
  value?: unknown;
  error?: unknown;
  revocation?: RevocationInfo;
  revokeRequested: boolean;
  controller: AbortController;
}

export class DependencyExecutor {
  private readonly nodes = new Map<NodeId, NodeRecord>();
  private readonly concurrency: number;
  private active = 0;
  private running = false;
  private edgesBuilt = false;
  private wake: (() => void) | null = null;

  constructor(options: ExecutorOptions = {}) {
    this.concurrency = options.concurrency ?? Number.POSITIVE_INFINITY;
    if (this.concurrency < 1) {
      throw new Error('concurrency must be >= 1');
    }
  }

  /**
   * 注册节点。deps 中重复的边会被去重（同一条边只计一次）。
   */
  addNode(id: NodeId, task: Task, deps: readonly NodeId[] = []): this {
    if (this.running) {
      throw new Error(`cannot add node while running: ${String(id)}`);
    }
    if (this.nodes.has(id)) {
      throw new Error(`duplicate node id: ${String(id)}`);
    }
    this.nodes.set(id, {
      id,
      task,
      deps: [...new Set(deps)],
      dependents: [],
      status: 'pending',
      revokeRequested: false,
      controller: new AbortController(),
    });
    this.edgesBuilt = false;
    return this;
  }

  /** 去重后的直接前驱列表。 */
  getDependencies(id: NodeId): readonly NodeId[] {
    return this.requireNode(id).deps;
  }

  getStatus(id: NodeId): NodeStatus {
    return this.requireNode(id).status;
  }

  /**
   * 撤销节点（幂等）。
   * - pending：立即撤销，并向未启动的后继传播；
   * - running：标记撤销请求并触发 AbortSignal，节点继续跑完并保留结果，
   *   未启动的后继立即随之撤销；
   * - succeeded / failed / revoked：无操作，返回 false。
   */
  revoke(id: NodeId, message?: string): boolean {
    this.buildEdges();
    const node = this.requireNode(id);
    if (node.status === 'pending') {
      node.status = 'revoked';
      node.revocation = {
        kind: 'manual',
        causes: [],
        primaryCause: node.id,
        chain: [node.id],
        message,
      };
      this.propagate(node);
      return true;
    }
    if (node.status === 'running') {
      node.revokeRequested = true;
      node.controller.abort();
      this.propagate(node);
      return true;
    }
    return false;
  }

  /** 运行整个图，直到所有节点进入终态（succeeded / failed / revoked）。 */
  async run(): Promise<RunReport> {
    if (this.running) {
      throw new Error('executor is already running');
    }
    this.buildEdges();
    this.validate();
    this.running = true;
    try {
      for (;;) {
        let started = false;
        for (const node of this.nodes.values()) {
          if (this.active >= this.concurrency) break;
          if (node.status !== 'pending') continue;
          const ready = node.deps.every(
            (dep) => this.nodes.get(dep)!.status === 'succeeded',
          );
          if (ready) {
            this.start(node);
            started = true;
          }
        }
        if (this.active === 0) {
          const anyPending = [...this.nodes.values()].some(
            (node) => node.status === 'pending',
          );
          if (!anyPending) break;
          if (!started) {
            // 不可达：有前驱失败/撤销时 propagate 已同步撤销后继。
            throw new Error('scheduler deadlock: pending nodes cannot start');
          }
          continue;
        }
        await this.waitForSettle();
      }
    } finally {
      this.running = false;
    }
    return this.report();
  }

  private start(node: NodeRecord): void {
    node.status = 'running';
    this.active += 1;
    void (async () => {
      try {
        node.value = await node.task({
          id: node.id,
          signal: node.controller.signal,
        });
        // 与撤销的竞争：任务完成时若节点仍是自己（running），结果生效；
        // 已启动节点不会被改成 revoked，结果始终保留。
        node.status = 'succeeded';
      } catch (error) {
        node.error = error;
        node.status = 'failed';
        this.propagate(node);
      } finally {
        this.active -= 1;
        this.notify();
      }
    })();
  }

  /**
   * 从 from 出发向后传播撤销：
   * - pending 后继：撤销并计算原因链，继续递归；
   * - 已撤销后继：前驱集合可能扩大（多个依赖同时失败），重算主因与原因链；
   * - running / 终态后继：不动，保留自身结果。
   */
  private propagate(from: NodeRecord): void {
    for (const nextId of from.dependents) {
      const next = this.nodes.get(nextId)!;
      if (next.status === 'pending') {
        next.status = 'revoked';
        this.computeRevocation(next);
        this.propagate(next);
      } else if (
        next.status === 'revoked' &&
        next.revocation?.kind === 'propagated'
      ) {
        if (this.computeRevocation(next)) {
          this.propagate(next);
        }
      }
    }
  }

  /** 根据当前所有“坏”前驱重算撤销信息，返回是否有变化。 */
  private computeRevocation(node: NodeRecord): boolean {
    const causes = node.deps.filter((dep) =>
      this.isBadCause(this.nodes.get(dep)!),
    );
    if (causes.length === 0) return false;
    const primary = causes.reduce((a, b) =>
      compareNodeIds(a, b) <= 0 ? a : b,
    );
    const chain = [...this.chainOf(this.nodes.get(primary)!), node.id];
    const prev = node.revocation;
    const changed =
      !prev ||
      prev.primaryCause !== primary ||
      prev.causes.length !== causes.length ||
      !prev.causes.every((cause) => causes.includes(cause));
    node.revocation = { kind: 'propagated', causes, primaryCause: primary, chain };
    return changed;
  }

  private isBadCause(node: NodeRecord): boolean {
    if (node.status === 'failed' || node.status === 'revoked') return true;
    return node.status === 'running' && node.revokeRequested;
  }

  /** 节点作为原因时，到它为止的原因链。 */
  private chainOf(node: NodeRecord): NodeId[] {
    if (node.status === 'revoked' && node.revocation) {
      return node.revocation.chain;
    }
    return [node.id];
  }

  private buildEdges(): void {
    if (this.edgesBuilt) return;
    for (const node of this.nodes.values()) {
      node.dependents = [];
    }
    for (const node of this.nodes.values()) {
      for (const dep of node.deps) {
        const target = this.nodes.get(dep);
        if (target) target.dependents.push(node.id);
      }
    }
    this.edgesBuilt = true;
  }

  private validate(): void {
    for (const node of this.nodes.values()) {
      for (const dep of node.deps) {
        if (!this.nodes.has(dep)) {
          throw new Error(
            `node ${String(node.id)} depends on unknown node ${String(dep)}`,
          );
        }
      }
    }
    // 环检测：三色 DFS
    const color = new Map<NodeId, 0 | 1 | 2>();
    const visit = (id: NodeId, stack: NodeId[]): void => {
      color.set(id, 1);
      for (const dep of this.nodes.get(id)!.deps) {
        const state = color.get(dep) ?? 0;
        if (state === 1) {
          const full = [...stack, id];
          const cycle = [...full.slice(full.indexOf(dep)), dep]
            .map(String)
            .join(' -> ');
          throw new Error(`dependency cycle detected: ${cycle}`);
        }
        if (state === 0) visit(dep, [...stack, id]);
      }
      color.set(id, 2);
    };
    for (const id of this.nodes.keys()) {
      if ((color.get(id) ?? 0) === 0) visit(id, []);
    }
  }

  private waitForSettle(): Promise<void> {
    return new Promise((resolve) => {
      this.wake = resolve;
    });
  }

  private notify(): void {
    const wake = this.wake;
    this.wake = null;
    wake?.();
  }

  private report(): RunReport {
    const results = new Map<NodeId, NodeResult>();
    const succeeded: NodeId[] = [];
    const failed: NodeId[] = [];
    const revoked: NodeId[] = [];
    for (const node of this.nodes.values()) {
      results.set(node.id, {
        id: node.id,
        status: node.status,
        value: node.value,
        error: node.error,
        revocation: node.revocation,
        revokeRequested: node.revokeRequested,
      });
      if (node.status === 'succeeded') succeeded.push(node.id);
      else if (node.status === 'failed') failed.push(node.id);
      else if (node.status === 'revoked') revoked.push(node.id);
    }
    return { results, succeeded, failed, revoked };
  }

  private requireNode(id: NodeId): NodeRecord {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node: ${String(id)}`);
    return node;
  }
}
