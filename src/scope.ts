import { Scheduler } from "./scheduler.ts";
import { Ledger } from "./ledger.ts";
import { Store } from "./journal.ts";
import type { Op, Reducer } from "./journal.ts";
import {
  AcquireCancelledError,
  DependencyCycleError,
  ResourceReleaseError,
  ScopeCloseError,
  ScopeClosedError,
} from "./errors.ts";

export type Phase = "open" | "closing" | "closed";

export type ResourceStatus =
  | "acquiring"
  | "owned"
  | "releasing"
  | "released"
  | "release-failed"
  | "acquire-failed"
  | "returned-late";

export interface ResourceState {
  id: string;
  name: string;
  status: ResourceStatus;
  deps: string[];
  borrowedBy: string[];
}

export interface ScopeState {
  phase: Phase;
  resources: Record<string, ResourceState>;
}

export interface ScopeEvent {
  scope: string;
  op: Op;
  state: ScopeState;
}

export interface ResourceSpec<T> {
  name?: string;
  acquire: () => T | Promise<T>;
  release: (value: T) => void | Promise<void>;
  /** Handles owned by the same scope that must be released after this one. */
  deps?: Handle<unknown>[];
}

export interface Handle<T> {
  readonly id: string;
  readonly name: string;
  readonly owner: Scope;
  /** Resolves with the owned value; rejects on cancel / scope close / acquire failure. */
  readonly value: Promise<T>;
  readonly status: ResourceStatus;
  /** Cancel a pending acquisition. A late-arriving value is returned immediately. */
  cancel(): void;
}

interface ResourceRecord {
  id: string;
  name: string;
  deps: string[];
  status: ResourceStatus;
  value: unknown;
  release: (value: unknown) => void | Promise<void>;
  cancelled: boolean;
  resolve: (value: unknown) => void;
  reject: (err: unknown) => void;
}

const reduceScope: Reducer<ScopeState> = (state, op) => {
  switch (op.type) {
    case "register": {
      const record: ResourceState = {
        id: op.id as string,
        name: op.name as string,
        status: "acquiring",
        deps: [...(op.deps as string[])],
        borrowedBy: [],
      };
      return { ...state, resources: { ...state.resources, [record.id]: record } };
    }
    case "status": {
      const id = op.id as string;
      const prev = state.resources[id];
      if (!prev) return state;
      return {
        ...state,
        resources: {
          ...state.resources,
          [id]: { ...prev, status: op.status as ResourceStatus },
        },
      };
    }
    case "add-dep": {
      const id = op.id as string;
      const prev = state.resources[id];
      if (!prev) return state;
      return {
        ...state,
        resources: {
          ...state.resources,
          [id]: { ...prev, deps: [...prev.deps, op.dep as string] },
        },
      };
    }
    case "borrow": {
      const id = op.id as string;
      const prev = state.resources[id];
      if (!prev) return state;
      return {
        ...state,
        resources: {
          ...state.resources,
          [id]: { ...prev, borrowedBy: [...prev.borrowedBy, op.by as string] },
        },
      };
    }
    case "phase":
      return { ...state, phase: op.phase as Phase };
    default:
      return state;
  }
};

export interface ScopeOptions {
  id?: string;
  parent?: Scope;
  scheduler?: Scheduler;
  ledger?: Ledger;
}

let scopeSeq = 0;

export class Scope {
  readonly id: string;
  readonly store: Store<ScopeState>;

  #scheduler: Scheduler;
  #ledger: Ledger;
  #parent: Scope | undefined;
  #children = new Set<Scope>();
  #childSeq = 0;
  #records = new Map<string, ResourceRecord>();
  #order: string[] = [];
  #phase: Phase = "open";
  #closing: Promise<void> | undefined;
  #inflight = new Set<Promise<unknown>>();
  #lateErrors: unknown[] = [];
  #seq = 0;

  constructor(opts: ScopeOptions = {}) {
    this.id = opts.id ?? `scope-${++scopeSeq}`;
    this.#scheduler = opts.scheduler ?? new Scheduler({ auto: true });
    this.#ledger = opts.ledger ?? new Ledger();
    this.#parent = opts.parent;
    this.store = new Store<ScopeState>(reduceScope, {
      phase: "open",
      resources: {},
    });
    if (this.#parent) this.#parent.#children.add(this);
  }

  get phase(): Phase {
    return this.#phase;
  }

  /** Current committed state snapshot (frozen; readers never see partial transactions). */
  get state(): ScopeState {
    return this.store.snapshot();
  }

  /** Subscribe to committed lifecycle events of this node. Unsubscribe is idempotent. */
  subscribe(listener: (event: ScopeEvent) => void): () => void {
    return this.store.subscribe((state, op) =>
      listener({ scope: this.id, op, state }),
    );
  }

  /** Create a child scope. Closing the parent closes children first. */
  fork(id?: string): Scope {
    return new Scope({
      id: id ?? `${this.id}/s${++this.#childSeq}`,
      parent: this,
      scheduler: this.#scheduler,
      ledger: this.#ledger,
    });
  }

  /**
   * Register and asynchronously acquire a resource. The slot is reserved
   * synchronously; the acquisition itself is scheduled. If the scope closes
   * (or the handle is cancelled) before the value arrives, the late value is
   * released immediately upon arrival — ownership is never leaked.
   */
  acquire<T>(spec: ResourceSpec<T>): Handle<T> {
    if (this.#phase !== "open") throw new ScopeClosedError(this.id);
    const id = `${this.id}:r${++this.#seq}`;
    const name = spec.name ?? id;
    const depHandles = spec.deps ?? [];
    for (const dep of depHandles) {
      if (dep.owner !== this) {
        throw new Error(
          `dependency "${dep.id}" must be owned by scope "${this.id}"; use borrow() across scopes`,
        );
      }
    }
    const deps = depHandles.map((dep) => dep.id);

    let resolveFn: (value: unknown) => void = () => {};
    let rejectFn: (err: unknown) => void = () => {};
    const settled = new Promise<T>((resolve, reject) => {
      resolveFn = resolve as (value: unknown) => void;
      rejectFn = reject;
    });
    // Rejections are also observable via close(); avoid unhandled-rejection noise.
    settled.catch(() => {});

    const record: ResourceRecord = {
      id,
      name,
      deps,
      status: "acquiring",
      value: undefined,
      release: spec.release as (value: unknown) => void | Promise<void>,
      cancelled: false,
      resolve: resolveFn,
      reject: rejectFn,
    };
    this.#records.set(id, record);
    this.#order.push(id);
    this.store.transact([{ type: "register", id, name, deps }]);

    const job = this.#scheduler.task(async () => {
      let value: T;
      try {
        value = await spec.acquire();
      } catch (err) {
        record.status = "acquire-failed";
        this.store.transact([{ type: "status", id, status: "acquire-failed" }]);
        record.reject(err);
        return;
      }
      this.#ledger.recordAcquire(id);
      if (record.cancelled || this.#phase !== "open") {
        // Late arrival: ownership must be returned immediately.
        record.status = "returned-late";
        this.store.transact([{ type: "status", id, status: "returned-late" }]);
        this.#ledger.recordRelease(id, "late-return");
        try {
          await spec.release(value);
        } catch (err) {
          this.#lateErrors.push(new ResourceReleaseError(id, err));
        }
        record.reject(
          record.cancelled
            ? new AcquireCancelledError(name)
            : new ScopeClosedError(this.id),
        );
        return;
      }
      record.value = value;
      record.status = "owned";
      this.store.transact([{ type: "status", id, status: "owned" }]);
      record.resolve(value);
    });
    this.#inflight.add(job);
    const forget = () => this.#inflight.delete(job);
    job.then(forget, forget);

    const owner = this;
    return {
      id,
      name,
      owner,
      value: settled,
      get status() {
        return record.status;
      },
      cancel() {
        if (record.status === "acquiring" && !record.cancelled) {
          record.cancelled = true;
          record.reject(new AcquireCancelledError(name));
        }
      },
    };
  }

  /**
   * Add a dependency edge between two resources of this scope. Edges are
   * recorded in the journal; cycles are tolerated and broken deterministically
   * at close time (see close()).
   */
  addDependency(handle: Handle<unknown>, dep: Handle<unknown>): void {
    if (this.#phase !== "open") throw new ScopeClosedError(this.id);
    if (handle.owner !== this || dep.owner !== this) {
      throw new Error(`dependencies must be owned by scope "${this.id}"`);
    }
    const record = this.#records.get(handle.id);
    if (!record) throw new Error(`unknown resource "${handle.id}"`);
    record.deps.push(dep.id);
    this.store.transact([{ type: "add-dep", id: handle.id, dep: dep.id }]);
  }

  /**
   * Borrow a resource owned by this scope or an ancestor. The borrower never
   * releases it; closing the borrower leaves the owner's resource untouched.
   */
  async borrow<T>(handle: Handle<T>): Promise<T> {
    if (this.#phase !== "open") throw new ScopeClosedError(this.id);
    if (handle.owner.#phase !== "open") throw new ScopeClosedError(handle.owner.id);
    let node: Scope | undefined = this;
    let reachable = false;
    while (node) {
      if (node === handle.owner) {
        reachable = true;
        break;
      }
      node = node.#parent;
    }
    if (!reachable) {
      throw new Error(
        `cannot borrow "${handle.id}": owner "${handle.owner.id}" is not an ancestor of "${this.id}"`,
      );
    }
    handle.owner.store.transact([{ type: "borrow", id: handle.id, by: this.id }]);
    return handle.value;
  }

  /**
   * Close the scope. Idempotent: every call returns the same promise.
   * Order: children first, then own resources in reverse dependency order
   * (dependents before dependencies, ties broken LIFO by registration).
   * Release failures never stop the sweep; all errors are aggregated into a
   * ScopeCloseError. In-flight acquisitions are awaited and their late
   * values returned immediately, so nothing leaks.
   */
  close(): Promise<void> {
    if (!this.#closing) {
      this.#closing = this.#doClose();
      // The aggregate is for the caller(s) to inspect; avoid unhandled noise.
      this.#closing.catch(() => {});
    }
    return this.#closing;
  }

  async #doClose(): Promise<void> {
    this.#phase = "closing";
    this.store.transact([{ type: "phase", phase: "closing" }]);
    const errors: unknown[] = [];

    // 1. Children are dependents of this scope: close them first.
    for (const child of [...this.#children].reverse()) {
      try {
        await child.close();
      } catch (err) {
        errors.push(err);
      }
    }

    // 2. Reject waiters of still-pending acquisitions. Their late values
    //    will be returned immediately by the acquisition job itself.
    for (const record of this.#records.values()) {
      if (record.status === "acquiring") {
        record.reject(new ScopeClosedError(this.id));
      }
    }

    // 3. Release owned resources in reverse dependency order.
    for (const id of this.#releaseOrder(errors)) {
      const record = this.#records.get(id);
      if (!record || record.status !== "owned") continue;
      record.status = "releasing";
      this.store.transact([{ type: "status", id, status: "releasing" }]);
      this.#ledger.recordRelease(id);
      try {
        await record.release(record.value);
        record.status = "released";
        this.store.transact([{ type: "status", id, status: "released" }]);
      } catch (err) {
        record.status = "release-failed";
        this.store.transact([{ type: "status", id, status: "release-failed" }]);
        errors.push(new ResourceReleaseError(id, err));
      }
    }

    // 4. Await in-flight acquisitions; late values are returned immediately
    //    inside their jobs. Failures of those late returns join the aggregate.
    await Promise.allSettled([...this.#inflight]);
    errors.push(...this.#lateErrors.splice(0));

    this.#phase = "closed";
    this.store.transact([{ type: "phase", phase: "closed" }]);
    if (this.#parent) this.#parent.#children.delete(this);

    if (errors.length > 0) throw new ScopeCloseError(this.id, errors);
  }

  /**
   * Deterministic release order: reverse topological order of the dependency
   * DAG (dependents first), ties broken by reverse registration order.
   * Cycles are broken deterministically — the back edge found by DFS (nodes
   * visited in registration order, edges in declaration order) is ignored and
   * reported as a DependencyCycleError; every node is still released exactly
   * once.
   */
  #releaseOrder(errors: unknown[]): string[] {
    const owned = this.#order.filter(
      (id) => this.#records.get(id)?.status === "owned",
    );
    const ownedSet = new Set(owned);
    const mark = new Map<string, 1 | 2>();
    const post: string[] = [];
    const reportedCycles = new Set<string>();

    const visit = (id: string, stack: string[]): void => {
      mark.set(id, 1);
      const record = this.#records.get(id);
      if (!record) return;
      for (const dep of record.deps) {
        if (!ownedSet.has(dep)) continue;
        const m = mark.get(dep);
        if (m === undefined) {
          visit(dep, [...stack, id]);
        } else if (m === 1) {
          const at = stack.indexOf(dep);
          const cycle = [...stack.slice(at < 0 ? 0 : at), id, dep];
          const key = cycle.join(">");
          if (!reportedCycles.has(key)) {
            reportedCycles.add(key);
            errors.push(new DependencyCycleError(cycle));
          }
        }
      }
      mark.set(id, 2);
      post.push(id);
    };

    for (const id of owned) {
      if (!mark.has(id)) visit(id, []);
    }
    return post.reverse();
  }
}
