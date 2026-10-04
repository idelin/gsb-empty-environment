import type {
  CancelRecord,
  FailureRecord,
  NodeRecord,
  NodeState,
  TaskFn,
} from './types.ts';

interface NodeEntry {
  id: number;
  name: string;
  task: TaskFn;
  deps: Set<number>;
  dependents: Set<number>;
  state: NodeState;
  remaining: number;
  result: unknown;
  failure?: FailureRecord;
  cancellation?: CancelRecord;
  abort?: AbortController;
}

const isTerminal = (state: NodeState): boolean =>
  state === 'completed' || state === 'failed' || state === 'cancelled';

export class Executor {
  private readonly nodes: NodeEntry[] = [];
  private readonly byName = new Map<string, number>();
  private readonly concurrency: number;
  private running = 0;
  private started = false;
  private draining = false;
  private drainScheduled = false;
  private donePromise?: Promise<void>;
  private doneResolve?: () => void;

  constructor(options: { concurrency?: number } = {}) {
    this.concurrency = options.concurrency ?? Number.POSITIVE_INFINITY;
    if (this.concurrency < 1) throw new Error('concurrency must be >= 1');
  }

  add<T>(name: string, task: TaskFn<T>, deps: ReadonlyArray<number | string> = []): number {
    this.assertNotStarted();
    if (this.byName.has(name)) throw new Error(`duplicate node name: ${name}`);
    const id = this.nodes.length;
    this.nodes.push({
      id,
      name,
      task: task as TaskFn,
      deps: new Set(),
      dependents: new Set(),
      state: 'pending',
      remaining: 0,
      result: undefined,
    });
    this.byName.set(name, id);
    this.dependsOn(id, ...deps);
    return id;
  }

  dependsOn(node: number | string, ...deps: ReadonlyArray<number | string>): void {
    this.assertNotStarted();
    const id = this.resolve(node);
    const entry = this.nodes[id];
    for (const dep of deps) {
      const depId = this.resolve(dep);
      if (depId === id) {
        throw new Error(`dependency cycle detected: #${id} cannot depend on itself`);
      }
      if (entry.deps.has(depId)) continue; // 重复边去重，幂等
      if (this.transitivelyDependsOn(depId, id)) {
        throw new Error(`dependency cycle detected: #${id} -> #${depId}`);
      }
      entry.deps.add(depId);
      this.nodes[depId].dependents.add(id);
      entry.remaining += 1;
    }
  }

  dependencies(node: number | string): number[] {
    return [...this.nodes[this.resolve(node)].deps].sort((a, b) => a - b);
  }

  cancel(node: number | string, message = 'cancelled by user'): boolean {
    const entry = this.nodes[this.resolve(node)];
    if (entry.state !== 'pending' && entry.state !== 'running') return false;
    this.settleCancelled(entry, {
      node: entry.id,
      kind: 'explicit',
      message,
      sources: [],
    });
    return true;
  }

  run(): Promise<void> {
    if (!this.donePromise) {
      this.started = true;
      this.donePromise = new Promise<void>((resolve) => {
        this.doneResolve = resolve;
      });
      this.scheduleDrain();
    }
    return this.donePromise;
  }

  record(node: number | string): NodeRecord {
    const entry = this.nodes[this.resolve(node)];
    return {
      id: entry.id,
      name: entry.name,
      state: entry.state,
      result: entry.result,
      failure: entry.failure,
      cancellation: entry.cancellation,
    };
  }

  state(node: number | string): NodeState {
    return this.nodes[this.resolve(node)].state;
  }

  result<T = unknown>(node: number | string): T {
    const entry = this.nodes[this.resolve(node)];
    if (entry.state !== 'completed') {
      throw new Error(`node #${entry.id} (${entry.name}) is ${entry.state}, not completed`);
    }
    return entry.result as T;
  }

  snapshot(): NodeRecord[] {
    return this.nodes.map((_, id) => this.record(id));
  }

  private assertNotStarted(): void {
    if (this.started) throw new Error('executor already started; graph is frozen');
  }

  private resolve(node: number | string): number {
    const id = typeof node === 'number' ? node : this.byName.get(node);
    if (id === undefined || id < 0 || id >= this.nodes.length) {
      throw new Error(`unknown node: ${String(node)}`);
    }
    return id;
  }

  private transitivelyDependsOn(from: number, target: number): boolean {
    const seen = new Set<number>();
    const stack = [...this.nodes[from].deps];
    while (stack.length > 0) {
      const current = stack.pop()!;
      if (current === target) return true;
      if (seen.has(current)) continue;
      seen.add(current);
      for (const next of this.nodes[current].deps) stack.push(next);
    }
    return false;
  }

  private notifyDependents(entry: NodeEntry): void {
    for (const dependentId of entry.dependents) {
      this.nodes[dependentId].remaining -= 1;
    }
  }

  private settleCompleted(entry: NodeEntry, result: unknown): void {
    entry.state = 'completed';
    entry.result = result;
    this.running -= 1;
    this.notifyDependents(entry);
    this.scheduleDrain();
  }

  private settleFailed(entry: NodeEntry, error: unknown): void {
    entry.state = 'failed';
    entry.failure = { node: entry.id, error };
    this.running -= 1;
    this.notifyDependents(entry);
    this.scheduleDrain();
  }

  private settleCancelled(entry: NodeEntry, record: CancelRecord): void {
    if (entry.state === 'running') {
      this.running -= 1;
      entry.abort?.abort();
    }
    entry.state = 'cancelled';
    entry.cancellation = record;
    this.notifyDependents(entry);
    this.scheduleDrain();
  }

  private launch(entry: NodeEntry): void {
    entry.state = 'running';
    const abort = new AbortController();
    entry.abort = abort;
    this.running += 1;
    let outcome: Promise<unknown>;
    try {
      outcome = Promise.resolve(entry.task({ signal: abort.signal }));
    } catch (error) {
      this.settleFailed(entry, error);
      return;
    }
    outcome.then(
      (value) => {
        // 完成与撤销竞争：撤销先生效时，迟到的结果直接丢弃
        if (entry.state === 'running') this.settleCompleted(entry, value);
      },
      (error) => {
        if (entry.state === 'running') this.settleFailed(entry, error);
      },
    );
  }

  private dependencyCancelRecord(entry: NodeEntry, sources: number[]): CancelRecord {
    const primaryId = sources[0]; // sources 已按节点编号升序，最小编号为主因
    const primary = this.nodes[primaryId];
    const cause = primary.failure ?? primary.cancellation;
    const message =
      sources.length === 1
        ? `dependency #${primaryId} (${primary.name}) ${primary.state}`
        : `${sources.length} dependencies did not complete; primary cause: #${primaryId} (${primary.name})`;
    return {
      node: entry.id,
      kind: 'dependency',
      message,
      sources,
      primarySource: primaryId,
      cause,
    };
  }

  private scheduleDrain(): void {
    if (this.draining || this.drainScheduled) return;
    this.drainScheduled = true;
    queueMicrotask(() => {
      this.drainScheduled = false;
      this.drain();
    });
  }

  private drain(): void {
    if (this.draining) return;
    this.draining = true;
    try {
      let changed = true;
      while (changed) {
        changed = false;
        for (const entry of this.nodes) {
          if (entry.state !== 'pending') continue;
          const cancelledDeps: number[] = [];
          const failedDeps: number[] = [];
          for (const depId of entry.deps) {
            const depState = this.nodes[depId].state;
            if (depState === 'cancelled') cancelledDeps.push(depId);
            else if (depState === 'failed') failedDeps.push(depId);
          }
          // 撤销沿依赖立即传播；失败则等全部依赖落定，以收集同时失败的主因
          const shouldCancel =
            cancelledDeps.length > 0 ||
            (entry.remaining === 0 && failedDeps.length > 0);
          if (shouldCancel) {
            const sources = [...cancelledDeps, ...failedDeps].sort((a, b) => a - b);
            this.settleCancelled(entry, this.dependencyCancelRecord(entry, sources));
            changed = true;
          }
        }
        for (const entry of this.nodes) {
          if (this.running >= this.concurrency) break;
          if (entry.state !== 'pending' || entry.remaining !== 0) continue;
          let blocked = false;
          for (const depId of entry.deps) {
            const depState = this.nodes[depId].state;
            if (depState === 'failed' || depState === 'cancelled') {
              blocked = true;
              break;
            }
          }
          if (blocked) continue; // 下一轮不动点循环会将其撤销
          this.launch(entry);
          changed = true;
        }
      }
    } finally {
      this.draining = false;
    }
    if (this.doneResolve && this.nodes.every((entry) => isTerminal(entry.state))) {
      this.doneResolve();
    }
  }
}
