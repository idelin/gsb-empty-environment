import { batch, type Signal } from "./reactive.ts";

export interface Snapshot {
  readonly id: number;
  readonly label: string;
  readonly parentId: number | null;
  readonly childIds: readonly number[];
  readonly values: ReadonlyMap<number, unknown>;
}

interface SnapshotNode {
  id: number;
  label: string;
  parent: SnapshotNode | null;
  children: SnapshotNode[];
  preferredChild: SnapshotNode | null;
  values: Map<number, unknown>;
  captured: boolean;
}

function deepFreeze<T>(value: T, seen: Set<object> = new Set()): T {
  if (value !== null && typeof value === "object" && !seen.has(value)) {
    seen.add(value as object);
    for (const key of Reflect.ownKeys(value)) {
      deepFreeze((value as Record<PropertyKey, unknown>)[key], seen);
    }
    Object.freeze(value);
  }
  return value;
}

function deepClone<T>(value: T): T {
  if (value === null || typeof value !== "object") return value;
  if (Array.isArray(value)) return value.map(deepClone) as T;
  if (value instanceof Map) {
    return new Map([...value].map(([k, v]) => [deepClone(k), deepClone(v)])) as T;
  }
  if (value instanceof Set) {
    return new Set([...value].map(deepClone)) as T;
  }
  const out: Record<PropertyKey, unknown> = {};
  for (const key of Reflect.ownKeys(value)) {
    out[key] = deepClone((value as Record<PropertyKey, unknown>)[key]);
  }
  return out as T;
}

export class TimeTravelController {
  private readonly signals = new Map<number, Signal<unknown>>();
  private readonly nodes = new Map<number, SnapshotNode>();
  private readonly root: SnapshotNode;
  private current: SnapshotNode;
  private nextNodeId = 0;
  private restoring = false;
  private transactionDepth = 0;
  private transactionDirty = false;
  private readonly pendingPreState = new Map<number, unknown>();

  constructor() {
    this.root = this.createNode("initial", null, false);
    this.current = this.root;
  }

  register<T>(sig: Signal<T>): Signal<T> {
    this.signals.set(sig.id, sig as Signal<unknown>);
    sig.subscribe((_next, prev) => this.onSignalChange(sig.id, prev));
    return sig;
  }

  transaction<T>(label: string, fn: () => T): T {
    this.transactionDepth += 1;
    try {
      return batch(fn);
    } finally {
      this.transactionDepth -= 1;
      if (this.transactionDepth === 0 && this.transactionDirty) {
        this.transactionDirty = false;
        this.record(label);
      }
    }
  }

  undo(steps = 1): boolean {
    let moved = false;
    for (let i = 0; i < steps; i += 1) {
      const parent = this.current.parent;
      if (!parent) break;
      parent.preferredChild = this.current;
      this.current = parent;
      moved = true;
    }
    if (moved) this.restore();
    return moved;
  }

  redo(steps = 1): boolean {
    let moved = false;
    for (let i = 0; i < steps; i += 1) {
      const next =
        this.current.preferredChild ??
        this.current.children[this.current.children.length - 1];
      if (!next) break;
      this.current = next;
      moved = true;
    }
    if (moved) this.restore();
    return moved;
  }

  jumpTo(snapshotId: number): boolean {
    const node = this.nodes.get(snapshotId);
    if (!node || node === this.current) return false;
    this.current = node;
    this.restore();
    return true;
  }

  get canUndo(): boolean {
    return this.current.parent !== null;
  }

  get canRedo(): boolean {
    return (
      this.current.preferredChild !== null || this.current.children.length > 0
    );
  }

  get currentSnapshot(): Snapshot {
    return this.toSnapshot(this.current);
  }

  history(): Snapshot[] {
    const path: SnapshotNode[] = [];
    let node: SnapshotNode | null = this.current;
    while (node) {
      path.push(node);
      node = node.parent;
    }
    return path.reverse().map((n) => this.toSnapshot(n));
  }

  private onSignalChange(id: number, prev: unknown): void {
    if (this.restoring) return;
    if (!this.pendingPreState.has(id)) {
      this.pendingPreState.set(id, deepFreeze(deepClone(prev)));
    }
    if (this.transactionDepth > 0) {
      this.transactionDirty = true;
      return;
    }
    this.record("change");
  }

  private createNode(
    label: string,
    parent: SnapshotNode | null,
    capture: boolean,
  ): SnapshotNode {
    const node: SnapshotNode = {
      id: this.nextNodeId++,
      label,
      parent,
      children: [],
      preferredChild: null,
      values: new Map(),
      captured: false,
    };
    this.nodes.set(node.id, node);
    if (capture) this.capture(node);
    return node;
  }

  private capture(node: SnapshotNode): void {
    node.values.clear();
    for (const [id, sig] of this.signals) {
      node.values.set(id, deepFreeze(deepClone(sig.peek())));
    }
    node.captured = true;
  }

  private record(label: string): void {
    if (!this.current.captured) {
      for (const [id, sig] of this.signals) {
        const value = this.pendingPreState.has(id)
          ? this.pendingPreState.get(id)
          : sig.peek();
        this.current.values.set(id, deepFreeze(deepClone(value)));
      }
      this.current.captured = true;
    }
    this.pendingPreState.clear();
    const node = this.createNode(label, this.current, true);
    this.current.children.push(node);
    this.current = node;
  }

  private restore(): void {
    if (!this.current.captured) this.capture(this.current);
    this.restoring = true;
    try {
      batch(() => {
        for (const [id, sig] of this.signals) {
          if (this.current.values.has(id)) {
            sig.set(deepClone(this.current.values.get(id)));
          }
        }
      });
    } finally {
      this.restoring = false;
    }
  }

  private toSnapshot(node: SnapshotNode): Snapshot {
    return {
      id: node.id,
      label: node.label,
      parentId: node.parent?.id ?? null,
      childIds: Object.freeze(node.children.map((c) => c.id)),
      values: node.values,
    };
  }
}
