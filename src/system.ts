import type { Lattice } from "./lattice.ts";
import type { Store } from "./store.ts";

export class NonMonotoneError extends Error {
  readonly cellName: string;

  constructor(cellName: string, message: string) {
    super(message);
    this.name = "NonMonotoneError";
    this.cellName = cellName;
  }
}

export interface DeclareOptions {
  assumeMonotone?: boolean;
}

interface Cell {
  name: string;
  lattice: Lattice<any>;
  deps: string[];
  fn: (...args: any[]) => any;
  assumeMonotone: boolean;
  verified: boolean;
}

export type SolveStatus = "converged" | "exhausted";
export type Scheduler = (ready: readonly string[]) => number;

export const schedulers = {
  fifo: ((_ready: readonly string[]) => 0) as Scheduler,
  lifo: ((ready: readonly string[]) => ready.length - 1) as Scheduler,
  random: (seed: number): Scheduler => {
    let state = seed >>> 0;
    return (ready) => {
      state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
      return state % ready.length;
    };
  },
};

export interface TraceEntry {
  readonly cell: string;
  readonly from: unknown;
  readonly to: unknown;
}

export interface SolveOptions {
  budget?: number;
  scheduler?: Scheduler;
  store?: Store;
  trace?: boolean;
}

export interface SolveResult {
  readonly status: SolveStatus;
  readonly converged: boolean;
  readonly evaluations: number;
  readonly values: ReadonlyMap<string, unknown>;
  readonly trace: readonly TraceEntry[];
}

const DEFAULT_BUDGET = 10_000;
const MAX_VERIFY_TUPLES = 512;

function cartesian(domains: readonly (readonly unknown[])[]): unknown[][] {
  let acc: unknown[][] = [[]];
  for (const domain of domains) {
    const next: unknown[][] = [];
    for (const tuple of acc) for (const value of domain) next.push([...tuple, value]);
    acc = next;
  }
  return acc;
}

function format(lattice: Lattice<any>, value: unknown): string {
  return lattice.show ? lattice.show(value) : String(value);
}

function verifyMonotone(cell: Cell, cells: ReadonlyMap<string, Cell>): void {
  const depLattices = cell.deps.map((dep) => cells.get(dep)!.lattice);
  const tuples = cartesian(depLattices.map((lattice) => lattice.elements()));
  if (tuples.length > MAX_VERIFY_TUPLES) {
    throw new Error(
      `cannot verify monotonicity of "${cell.name}": ${tuples.length} input ` +
        `combinations exceed limit ${MAX_VERIFY_TUPLES}; declare with { assumeMonotone: true }`,
    );
  }
  for (const a of tuples) {
    for (const b of tuples) {
      if (a === b) continue;
      const comparable = depLattices.every((lattice, i) => lattice.leq(a[i], b[i]));
      if (!comparable) continue;
      const fa = cell.fn(...a);
      const fb = cell.fn(...b);
      if (!cell.lattice.leq(fa, fb)) {
        const showTuple = (tuple: unknown[]) =>
          `[${tuple.map((v, i) => format(depLattices[i], v)).join(", ")}]`;
        throw new NonMonotoneError(
          cell.name,
          `cell "${cell.name}" is not monotone: ${showTuple(a)} <= ${showTuple(b)} but ` +
            `f(${showTuple(a)}) = ${format(cell.lattice, fa)} !<= ` +
            `f(${showTuple(b)}) = ${format(cell.lattice, fb)}`,
        );
      }
    }
  }
}

export class ComputationRing {
  private cells = new Map<string, Cell>();

  declare<T>(
    name: string,
    lattice: Lattice<T>,
    deps: readonly string[],
    fn: (...args: any[]) => T,
    options: DeclareOptions = {},
  ): this {
    if (this.cells.has(name)) throw new Error(`duplicate cell: ${name}`);
    this.cells.set(name, {
      name,
      lattice,
      deps: [...deps],
      fn,
      assumeMonotone: options.assumeMonotone ?? false,
      verified: false,
    });
    return this;
  }

  private verifyAll(): void {
    for (const cell of this.cells.values()) {
      for (const dep of cell.deps) {
        if (!this.cells.has(dep)) {
          throw new Error(`cell "${cell.name}" depends on unknown cell "${dep}"`);
        }
      }
    }
    for (const cell of this.cells.values()) {
      if (cell.verified || cell.assumeMonotone) continue;
      verifyMonotone(cell, this.cells);
      cell.verified = true;
    }
  }

  solve(options: SolveOptions = {}): SolveResult {
    this.verifyAll();
    const budget = options.budget ?? DEFAULT_BUDGET;
    const pick = options.scheduler ?? schedulers.fifo;
    const values = new Map<string, unknown>();
    const dependents = new Map<string, string[]>();
    for (const cell of this.cells.values()) {
      values.set(cell.name, cell.lattice.bottom);
      for (const dep of cell.deps) {
        let list = dependents.get(dep);
        if (!list) dependents.set(dep, (list = []));
        list.push(cell.name);
      }
    }
    const ready: string[] = [...this.cells.keys()];
    const trace: TraceEntry[] = [];
    let evaluations = 0;
    let status: SolveStatus = "converged";

    const iterate = (tx?: { set(key: string, value: unknown): void }): void => {
      while (ready.length > 0) {
        if (evaluations >= budget) {
          status = "exhausted";
          return;
        }
        const index = pick(ready);
        const name = ready.splice(index, 1)[0];
        const cell = this.cells.get(name)!;
        const next = cell.fn(...cell.deps.map((dep) => values.get(dep)));
        evaluations++;
        const prev = values.get(name);
        if (!cell.lattice.leq(prev, next)) {
          throw new NonMonotoneError(
            name,
            `cell "${name}" decreased at runtime: ` +
              `${format(cell.lattice, prev)} !<= ${format(cell.lattice, next)}`,
          );
        }
        if (!cell.lattice.equals(prev, next)) {
          values.set(name, next);
          trace.push({ cell: name, from: prev, to: next });
          tx?.set(name, next);
          for (const dependent of dependents.get(name) ?? []) {
            if (!ready.includes(dependent)) ready.push(dependent);
          }
        }
      }
    };

    if (options.store) {
      options.store.transaction((tx) => {
        iterate(tx);
        tx.set("__status__", status);
      });
    } else {
      iterate();
    }

    return { status, converged: status === "converged", evaluations, values, trace };
  }
}
