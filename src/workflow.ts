/**
 * 工作流状态库：条件分支 + 汇合（join）。
 *
 * 核心语义：
 * 1. 分支激活时一次性评估条件，冻结选中集合，仅启动选中的子任务；
 *    未选中的分支永远保持 pending，汇合不会等待它们。
 * 2. 汇合只等待“实际启动的集合”全部到达终态（completed / cancelled），
 *    且每个分支的汇合事件恰好触发一次（由分支进入终态保证）。
 * 3. 重复的完成事件被忽略，不改变任何状态。
 * 4. 取消是终态且优先于迟到事件：取消后迟到的成功只记入 evidence，
 *    不改变状态、不推进流程。
 * 5. 分支可任意嵌套：内层分支作为外层分支的子节点参与外层汇合。
 */

export type Condition<C> = (ctx: C) => boolean;

export interface TaskDef<C> {
  readonly kind: 'task';
  readonly id: string;
}

export interface BranchChild<C> {
  readonly node: NodeDef<C>;
  readonly when?: Condition<C>;
}

export interface BranchDef<C> {
  readonly kind: 'branch';
  readonly id: string;
  readonly children: readonly BranchChild<C>[];
}

export type NodeDef<C> = TaskDef<C> | BranchDef<C>;

export const task = <C = unknown>(id: string): TaskDef<C> => ({ kind: 'task', id });

export const branch = <C = unknown>(
  id: string,
  children: readonly BranchChild<C>[],
): BranchDef<C> => ({ kind: 'branch', id, children });

export const when = <C>(node: NodeDef<C>, cond?: Condition<C>): BranchChild<C> => ({
  node,
  when: cond,
});

export type Status = 'pending' | 'active' | 'completed' | 'cancelled';

export type EngineEvent =
  | { readonly type: 'task.completed'; readonly taskId: string }
  | { readonly type: 'cancel'; readonly nodeId: string };

export type Emitted =
  | { readonly type: 'task.started'; readonly taskId: string }
  | { readonly type: 'branch.selected'; readonly branchId: string; readonly selected: readonly string[] }
  | {
      readonly type: 'branch.joined';
      readonly branchId: string;
      readonly selected: readonly string[];
      readonly outcome: 'completed' | 'cancelled';
    }
  | { readonly type: 'workflow.finished'; readonly outcome: 'completed' | 'cancelled' }
  | { readonly type: 'evidence.recorded'; readonly taskId: string; readonly kind: 'late-success' };

export interface Evidence {
  readonly kind: 'late-success';
  readonly taskId: string;
  /** 单调递增的事件序号，标记迟到成功到达时的位置。 */
  readonly seq: number;
}

interface RuntimeNode<C> {
  readonly def: NodeDef<C>;
  readonly parent: RuntimeNode<C> | null;
  status: Status;
  /** 定义的全部子节点（仅分支）。 */
  readonly children: RuntimeNode<C>[];
  /** 激活时冻结的选中集合；未激活为 null。 */
  selected: readonly RuntimeNode<C>[] | null;
}

const isTerminal = (s: Status): boolean => s === 'completed' || s === 'cancelled';

export class Workflow<C = unknown> {
  private readonly root: RuntimeNode<C>;
  private readonly byId = new Map<string, RuntimeNode<C>>();
  private readonly ctx: C;
  private seq = 0;
  private readonly emittedLog: Emitted[] = [];
  private readonly evidenceLog: Evidence[] = [];

  constructor(def: NodeDef<C>, ctx: C) {
    this.ctx = ctx;
    this.root = this.build(def, null);
    this.activate(this.root);
  }

  get emitted(): readonly Emitted[] {
    return this.emittedLog;
  }

  get evidence(): readonly Evidence[] {
    return this.evidenceLog;
  }

  get status(): Status {
    return this.root.status;
  }

  get done(): boolean {
    return isTerminal(this.root.status);
  }

  nodeStatus(id: string): Status | undefined {
    return this.byId.get(id)?.status;
  }

  /** 分支激活时冻结的选中集合（子节点 id）；未激活返回 undefined。 */
  selectedOf(branchId: string): readonly string[] | undefined {
    const node = this.byId.get(branchId);
    return node?.selected?.map((child) => child.def.id);
  }

  /** 处理一个外部事件，返回本次事件新触发的 Emitted 列表。 */
  dispatch(event: EngineEvent): Emitted[] {
    const before = this.emittedLog.length;
    this.seq += 1;
    switch (event.type) {
      case 'task.completed':
        this.onTaskCompleted(event.taskId);
        break;
      case 'cancel':
        this.onCancel(event.nodeId);
        break;
    }
    return this.emittedLog.slice(before);
  }

  private build(def: NodeDef<C>, parent: RuntimeNode<C> | null): RuntimeNode<C> {
    if (this.byId.has(def.id)) throw new Error(`duplicate node id: ${def.id}`);
    const node: RuntimeNode<C> = { def, parent, status: 'pending', children: [], selected: null };
    this.byId.set(def.id, node);
    if (def.kind === 'branch') {
      node.children.push(...def.children.map((child) => this.build(child.node, node)));
    }
    return node;
  }

  private emit(event: Emitted): void {
    this.emittedLog.push(event);
  }

  private activate(node: RuntimeNode<C>): void {
    if (node.status !== 'pending') return;
    node.status = 'active';
    if (node.def.kind === 'task') {
      this.emit({ type: 'task.started', taskId: node.def.id });
      return;
    }
    // 分支：条件在此刻评估一次，选中集合立即冻结，之后 ctx 变化不影响。
    const selected = node.def.children
      .map((child, index) => ({ when: child.when, runtime: node.children[index]! }))
      .filter((entry) => entry.when === undefined || entry.when(this.ctx))
      .map((entry) => entry.runtime);
    node.selected = Object.freeze(selected.slice());
    this.emit({
      type: 'branch.selected',
      branchId: node.def.id,
      selected: selected.map((child) => child.def.id),
    });
    for (const child of selected) this.activate(child);
    // 空选择立即汇合；非空选择通常在此时尚未汇合。
    this.maybeJoin(node);
  }

  /**
   * 汇合检查：仅当分支仍处于 active 且选中集合全部到达终态时汇合。
   * 汇合后分支进入终态，因此汇合事件对每个分支恰好触发一次。
   */
  private maybeJoin(node: RuntimeNode<C>): void {
    if (node.def.kind !== 'branch' || node.status !== 'active') return;
    const selected = node.selected ?? [];
    if (!selected.every((child) => isTerminal(child.status))) return;
    const outcome = selected.every((child) => child.status === 'completed') ? 'completed' : 'cancelled';
    node.status = outcome;
    this.emit({
      type: 'branch.joined',
      branchId: node.def.id,
      selected: selected.map((child) => child.def.id),
      outcome,
    });
    if (node.parent) {
      this.maybeJoin(node.parent);
    } else {
      this.emit({ type: 'workflow.finished', outcome });
    }
  }

  private onTaskCompleted(taskId: string): void {
    const node = this.byId.get(taskId);
    if (!node || node.def.kind !== 'task') return; // 未知事件：忽略
    switch (node.status) {
      case 'active':
        node.status = 'completed';
        if (node.parent) {
          this.maybeJoin(node.parent);
        } else {
          this.emit({ type: 'workflow.finished', outcome: 'completed' });
        }
        return;
      case 'cancelled': {
        // 取消后的迟到成功：只记证据，不推进流程。
        const entry: Evidence = { kind: 'late-success', taskId, seq: this.seq };
        this.evidenceLog.push(entry);
        this.emit({ type: 'evidence.recorded', taskId, kind: 'late-success' });
        return;
      }
      case 'completed':
      case 'pending':
        // 重复完成 / 从未启动：忽略。
        return;
    }
  }

  private onCancel(nodeId: string): void {
    const node = this.byId.get(nodeId);
    if (!node || isTerminal(node.status)) return; // 终态节点：取消为空操作
    this.cancelSubtree(node);
    if (node.parent) {
      this.maybeJoin(node.parent);
    } else {
      this.emit({ type: 'workflow.finished', outcome: 'cancelled' });
    }
  }

  private cancelSubtree(node: RuntimeNode<C>): void {
    if (isTerminal(node.status)) return; // 已完成的节点不被取消覆盖
    node.status = 'cancelled';
    for (const child of node.children) this.cancelSubtree(child);
  }
}
