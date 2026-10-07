/**
 * Workflow state library.
 *
 * Semantics:
 * - A branch starts only the subtasks whose condition holds at selection time.
 * - The selected set is frozen when the branch is evaluated; later context
 *   changes never alter it.
 * - A join (branch / all completion barrier) waits only for the actually
 *   started set; unselected branches can never block it. A join fires at
 *   most once.
 * - Duplicate completion events for the same task are ignored.
 * - After cancellation, a late success is recorded as evidence only and
 *   never advances the flow.
 */

export type NodeStatus = 'pending' | 'active' | 'completed' | 'cancelled' | 'skipped';

export interface TaskDef<C> {
  kind: 'task';
  id: string;
}

export interface AllDef<C> {
  kind: 'all';
  id: string;
  children: NodeDef<C>[];
}

export interface BranchOption<C> {
  id: string;
  when: (context: C) => boolean;
  node: NodeDef<C>;
}

export interface BranchDef<C> {
  kind: 'branch';
  id: string;
  options: BranchOption<C>[];
}

export type NodeDef<C> = TaskDef<C> | AllDef<C> | BranchDef<C>;

export function task<C>(id: string): TaskDef<C> {
  return { kind: 'task', id };
}

export function all<C>(id: string, children: NodeDef<C>[]): AllDef<C> {
  return { kind: 'all', id, children };
}

export function branch<C>(id: string, options: BranchOption<C>[]): BranchDef<C> {
  return { kind: 'branch', id, options };
}

export function option<C>(id: string, when: (context: C) => boolean, node: NodeDef<C>): BranchOption<C> {
  return { id, when, node };
}

export type WorkflowEvent =
  | { type: 'complete'; taskId: string }
  | { type: 'cancel'; nodeId: string };

export type TraceEntry =
  | { type: 'started'; nodeId: string }
  | { type: 'branch-selected'; nodeId: string; selected: string[]; skipped: string[] }
  | { type: 'join'; nodeId: string; waited: string[] }
  | { type: 'completed'; nodeId: string }
  | { type: 'cancelled'; nodeId: string }
  | { type: 'skipped'; nodeId: string }
  | { type: 'duplicate-complete'; taskId: string }
  | { type: 'ignored-cancel'; nodeId: string };

export type EvidenceReason = 'late-success-after-cancel' | 'not-active' | 'unknown-task';

export interface EvidenceEntry {
  taskId: string;
  reason: EvidenceReason;
}

interface RuntimeNode<C> {
  def: NodeDef<C>;
  id: string;
  status: NodeStatus;
  parent: RuntimeNode<C> | null;
  children: RuntimeNode<C>[];
  optionId: string | null;
  frozenSet: readonly string[] | null;
}

export class Workflow<C> {
  readonly trace: TraceEntry[] = [];
  readonly evidence: EvidenceEntry[] = [];

  private readonly context: C;
  private readonly nodes = new Map<string, RuntimeNode<C>>();
  private readonly root: RuntimeNode<C>;
  private startedFlag = false;

  constructor(def: NodeDef<C>, context: C) {
    this.context = context;
    this.root = this.build(def, null, null);
  }

  start(): void {
    if (this.startedFlag) return;
    this.startedFlag = true;
    this.activate(this.root);
  }

  dispatch(event: WorkflowEvent): void {
    if (event.type === 'complete') {
      this.completeTask(event.taskId);
    } else {
      this.cancelNode(event.nodeId);
    }
  }

  statusOf(nodeId: string): NodeStatus {
    const node = this.nodes.get(nodeId);
    if (!node) throw new Error(`unknown node: ${nodeId}`);
    return node.status;
  }

  frozenSetOf(branchId: string): readonly string[] {
    const node = this.nodes.get(branchId);
    if (!node || node.def.kind !== 'branch') throw new Error(`unknown branch: ${branchId}`);
    if (!node.frozenSet) throw new Error(`branch not selected yet: ${branchId}`);
    return node.frozenSet;
  }

  isDone(): boolean {
    return this.root.status === 'completed';
  }

  joinCount(nodeId: string): number {
    return this.trace.filter((e) => e.type === 'join' && e.nodeId === nodeId).length;
  }

  private build(def: NodeDef<C>, parent: RuntimeNode<C> | null, optionId: string | null): RuntimeNode<C> {
    const node: RuntimeNode<C> = {
      def,
      id: def.id,
      status: 'pending',
      parent,
      children: [],
      optionId,
      frozenSet: null,
    };
    if (this.nodes.has(node.id)) throw new Error(`duplicate node id: ${node.id}`);
    this.nodes.set(node.id, node);
    if (def.kind === 'all') {
      node.children = def.children.map((child) => this.build(child, node, null));
    } else if (def.kind === 'branch') {
      node.children = def.options.map((opt) => this.build(opt.node, node, opt.id));
    }
    return node;
  }

  private activate(node: RuntimeNode<C>): void {
    if (node.status !== 'pending') return;
    node.status = 'active';
    this.trace.push({ type: 'started', nodeId: node.id });
    const def = node.def;
    if (def.kind === 'all') {
      for (const child of node.children) this.activate(child);
      this.maybeJoin(node);
    } else if (def.kind === 'branch') {
      const selected: string[] = [];
      const skipped: string[] = [];
      for (const opt of def.options) {
        (opt.when(this.context) ? selected : skipped).push(opt.id);
      }
      node.frozenSet = Object.freeze(selected.slice());
      this.trace.push({
        type: 'branch-selected',
        nodeId: node.id,
        selected: selected.slice(),
        skipped: skipped.slice(),
      });
      for (const child of node.children) {
        if (child.optionId !== null && node.frozenSet.includes(child.optionId)) {
          this.activate(child);
        } else {
          this.markSkipped(child);
        }
      }
      this.maybeJoin(node);
    }
  }

  private markSkipped(node: RuntimeNode<C>): void {
    if (node.status !== 'pending') return;
    node.status = 'skipped';
    this.trace.push({ type: 'skipped', nodeId: node.id });
    for (const child of node.children) this.markSkipped(child);
  }

  private maybeJoin(node: RuntimeNode<C>): boolean {
    if (node.status !== 'active') return false;
    const def = node.def;
    if (def.kind === 'task') return false;
    let expected: RuntimeNode<C>[];
    if (def.kind === 'all') {
      expected = node.children;
    } else {
      const frozen = node.frozenSet ?? [];
      expected = node.children.filter((c) => c.optionId !== null && frozen.includes(c.optionId));
    }
    if (!expected.every((c) => c.status === 'completed')) return false;
    node.status = 'completed';
    this.trace.push({ type: 'join', nodeId: node.id, waited: expected.map((c) => c.id) });
    this.trace.push({ type: 'completed', nodeId: node.id });
    return true;
  }

  private completeTask(taskId: string): void {
    const node = this.nodes.get(taskId);
    if (!node || node.def.kind !== 'task') {
      this.evidence.push({ taskId, reason: 'unknown-task' });
      return;
    }
    if (node.status === 'completed') {
      this.trace.push({ type: 'duplicate-complete', taskId });
      return;
    }
    if (this.isInsideCancelled(node)) {
      this.evidence.push({ taskId, reason: 'late-success-after-cancel' });
      return;
    }
    if (node.status !== 'active') {
      this.evidence.push({ taskId, reason: 'not-active' });
      return;
    }
    node.status = 'completed';
    this.trace.push({ type: 'completed', nodeId: node.id });
    let cursor = node.parent;
    while (cursor) {
      if (!this.maybeJoin(cursor)) break;
      cursor = cursor.parent;
    }
  }

  private isInsideCancelled(node: RuntimeNode<C>): boolean {
    let cursor: RuntimeNode<C> | null = node;
    while (cursor) {
      if (cursor.status === 'cancelled') return true;
      cursor = cursor.parent;
    }
    return false;
  }

  private cancelNode(nodeId: string): void {
    const node = this.nodes.get(nodeId);
    if (!node) return;
    if (node.status === 'completed') {
      this.trace.push({ type: 'ignored-cancel', nodeId });
      return;
    }
    this.cancelSubtree(node);
  }

  private cancelSubtree(node: RuntimeNode<C>): void {
    if (node.status === 'completed' || node.status === 'cancelled') return;
    node.status = 'cancelled';
    this.trace.push({ type: 'cancelled', nodeId: node.id });
    for (const child of node.children) this.cancelSubtree(child);
  }
}

export function createWorkflow<C>(def: NodeDef<C>, context: C): Workflow<C> {
  return new Workflow(def, context);
}
