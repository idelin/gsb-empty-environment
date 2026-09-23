import type { Lattice } from './lattice.ts';

export type SchedulerKind = 'fifo' | 'lifo' | 'priority';

/** 声明单调、运行时却出现值下降时抛出 —— 非单调环被拒绝，而不是无限迭代。 */
export class NonMonotoneError extends Error {
  readonly node: string;
  readonly previous: unknown;
  readonly next: unknown;
  readonly iterations: number;
  constructor(node: string, previous: unknown, next: unknown, iterations: number) {
    super(
      `node "${node}" violated declared monotonicity after ${iterations} iteration(s): ` +
        `value would descend from ${JSON.stringify(previous)} to ${JSON.stringify(next)}`,
    );
    this.name = 'NonMonotoneError';
    this.node = node;
    this.previous = previous;
    this.next = next;
    this.iterations = iterations;
  }
}

/** 声明期错误（未声明单调、依赖不存在、写派生节点等）。 */
export class DeclarationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'DeclarationError';
  }
}

export type SettleStatus = 'converged' | 'non-converged';

export interface SettleResult {
  readonly status: SettleStatus;
  /** 实际执行的重算次数 */
  readonly iterations: number;
  /** 当前（可能未收敛的）值快照 */
  readonly values: ReadonlyMap<string, unknown>;
}

interface Scheduler {
  add(name: string): void;
  take(): string | undefined;
  readonly size: number;
}

class FifoScheduler implements Scheduler {
  private queue: string[] = [];
  private head = 0;
  private pending = new Set<string>();
  add(name: string): void {
    if (this.pending.has(name)) return;
    this.pending.add(name);
    this.queue.push(name);
  }
  take(): string | undefined {
    if (this.head >= this.queue.length) return undefined;
    const name = this.queue[this.head++];
    this.pending.delete(name);
    if (this.head > 64 && this.head * 2 > this.queue.length) {
      this.queue = this.queue.slice(this.head);
      this.head = 0;
    }
    return name;
  }
  get size(): number {
    return this.queue.length - this.head;
  }
}

class LifoScheduler implements Scheduler {
  private stack: string[] = [];
  private pending = new Set<string>();
  add(name: string): void {
    if (this.pending.has(name)) return;
    this.pending.add(name);
    this.stack.push(name);
  }
  take(): string | undefined {
    const name = this.stack.pop();
    if (name !== undefined) this.pending.delete(name);
    return name;
  }
  get size(): number {
    return this.stack.length;
  }
}

class PriorityScheduler implements Scheduler {
  private pending = new Set<string>();
  add(name: string): void {
    this.pending.add(name);
  }
  take(): string | undefined {
    let best: string | undefined;
    for (const name of this.pending) {
      if (best === undefined || name < best) best = name;
    }
    if (best !== undefined) this.pending.delete(best);
    return best;
  }
  get size(): number {
    return this.pending.size;
  }
}

type Compute = (values: readonly unknown[]) => unknown;

interface NodeEntry {
  readonly name: string;
  readonly kind: 'input' | 'derived';
  readonly lattice: Lattice<unknown>;
  value: unknown;
  readonly deps: readonly string[];
  readonly compute: Compute | null;
  /** 值的历史（含 bottom 起点），用于审计单调上升链 */
  readonly history: unknown[];
}

/**
 * 单调有限格不动点引擎。
 *
 * - 派生节点必须声明 `monotone: true` 才能参与（可成环）；
 * - `settle()` 做混沌迭代（chaotic iteration）：任意公平调度都收敛到同一最小不动点；
 * - 运行时守卫：任何节点的新值若不 >= 旧值，立即抛 NonMonotoneError；
 * - 迭代预算耗尽时返回 status: 'non-converged'，绝不死循环；
 * - 输入节点只增（join 语义），保证全系统单调、有限格上必然终止。
 */
export class FixpointEngine {
  private readonly nodes = new Map<string, NodeEntry>();
  private readonly budget: number;
  private readonly schedulerKind: SchedulerKind;

  constructor(options: { budget?: number; scheduler?: SchedulerKind } = {}) {
    this.budget = options.budget ?? 10_000;
    this.schedulerKind = options.scheduler ?? 'fifo';
  }

  /** 声明输入节点，初值缺省为格底元。 */
  input<T>(name: string, lattice: Lattice<T>, initial?: T): void {
    this.assertAbsent(name);
    const value: unknown = initial === undefined ? lattice.bottom : initial;
    this.nodes.set(name, {
      name,
      kind: 'input',
      lattice: lattice as Lattice<unknown>,
      value,
      deps: [],
      compute: null,
      history: [value],
    });
  }

  /** 声明派生节点。依赖按名引用，允许前向引用与成环；必须声明单调。 */
  derived<T>(
    name: string,
    lattice: Lattice<T>,
    deps: readonly string[],
    compute: (values: readonly unknown[]) => T,
    options: { monotone: boolean },
  ): void {
    this.assertAbsent(name);
    if (!options.monotone) {
      throw new DeclarationError(
        `node "${name}": derived nodes must be declared monotone to participate in cycles`,
      );
    }
    this.nodes.set(name, {
      name,
      kind: 'derived',
      lattice: lattice as Lattice<unknown>,
      value: lattice.bottom,
      deps: [...deps],
      compute: compute as Compute,
      history: [lattice.bottom],
    });
  }

  /** 单调写输入：新值 = join(旧值, value)，只升不降。 */
  setInput<T>(name: string, value: T): void {
    const node = this.mustHave(name);
    if (node.kind !== 'input') {
      throw new DeclarationError(`cannot set derived node "${name}"`);
    }
    const joined = node.lattice.join(node.value, value);
    if (!node.lattice.eq(node.value, joined)) {
      node.value = joined;
      node.history.push(joined);
    }
  }

  getValue<T>(name: string): T {
    return this.mustHave(name).value as T;
  }

  /** 节点值的完整上升历史（从 bottom 开始）。 */
  historyOf(name: string): readonly unknown[] {
    return this.mustHave(name).history;
  }

  /** 混沌迭代求最小不动点。 */
  settle(): SettleResult {
    const dependents = this.buildDependents();
    const scheduler = this.makeScheduler();
    for (const node of this.nodes.values()) {
      if (node.kind === 'derived') scheduler.add(node.name);
    }

    let iterations = 0;
    for (;;) {
      const name = scheduler.take();
      if (name === undefined) {
        return { status: 'converged', iterations, values: this.snapshot() };
      }
      if (iterations >= this.budget) {
        return { status: 'non-converged', iterations, values: this.snapshot() };
      }
      const node = this.nodes.get(name)!;
      const args = node.deps.map((dep) => this.nodes.get(dep)!.value);
      const next = node.compute!(args);
      if (!node.lattice.leq(node.value, next)) {
        throw new NonMonotoneError(name, node.value, next, iterations);
      }
      iterations++;
      if (!node.lattice.eq(node.value, next)) {
        node.value = next;
        node.history.push(next);
        for (const dependent of dependents.get(name)!) {
          scheduler.add(dependent);
        }
      }
    }
  }

  private buildDependents(): Map<string, string[]> {
    const dependents = new Map<string, string[]>();
    for (const name of this.nodes.keys()) dependents.set(name, []);
    for (const node of this.nodes.values()) {
      for (const dep of node.deps) {
        if (!this.nodes.has(dep)) {
          throw new DeclarationError(`node "${node.name}" depends on unknown node "${dep}"`);
        }
        dependents.get(dep)!.push(node.name);
      }
    }
    return dependents;
  }

  private makeScheduler(): Scheduler {
    switch (this.schedulerKind) {
      case 'fifo':
        return new FifoScheduler();
      case 'lifo':
        return new LifoScheduler();
      case 'priority':
        return new PriorityScheduler();
    }
  }

  private snapshot(): ReadonlyMap<string, unknown> {
    const out = new Map<string, unknown>();
    for (const [name, node] of this.nodes) out.set(name, node.value);
    return out;
  }

  private assertAbsent(name: string): void {
    if (this.nodes.has(name)) {
      throw new DeclarationError(`node "${name}" already declared`);
    }
  }

  private mustHave(name: string): NodeEntry {
    const node = this.nodes.get(name);
    if (!node) throw new DeclarationError(`unknown node "${name}"`);
    return node;
  }
}
