export type Phase = "capture" | "target" | "bubble";

export type NodeId = number;

export interface TreeEventInit<T = unknown> {
  type: string;
  data?: T;
  bubbles?: boolean;
}

export class TreeEvent<T = unknown> {
  readonly type: string;
  readonly data: T | undefined;
  readonly bubbles: boolean;
  target: NodeId | null = null;
  currentTarget: NodeId | null = null;
  phase: Phase | "none" = "none";
  private stopFlag = false;
  private immediateFlag = false;

  constructor(init: TreeEventInit<T>) {
    this.type = init.type;
    this.data = init.data;
    this.bubbles = init.bubbles ?? true;
  }

  get propagationStopped(): boolean {
    return this.stopFlag;
  }

  get immediatePropagationStopped(): boolean {
    return this.immediateFlag;
  }

  stopPropagation(): void {
    this.stopFlag = true;
  }

  stopImmediatePropagation(): void {
    this.immediateFlag = true;
    this.stopFlag = true;
  }
}

export type Listener<T = unknown> = (event: TreeEvent<T>) => void;

export interface AddListenerOptions {
  capture?: boolean;
  once?: boolean;
}

export interface RemoveListenerOptions {
  capture?: boolean;
}

export interface ListenerErrorInfo {
  node: NodeId;
  type: string;
  phase: Phase;
  error: unknown;
}

interface ListenerRecord {
  listener: Listener<never>;
  capture: boolean;
  once: boolean;
}

export interface EventTreeOptions {
  onListenerError?: (info: ListenerErrorInfo) => void;
}

export class EventTree {
  private nextId: NodeId = 1;
  private parents = new Map<NodeId, NodeId | null>();
  private listeners = new Map<NodeId, Map<string, ListenerRecord[]>>();
  private onListenerError?: (info: ListenerErrorInfo) => void;

  constructor(options: EventTreeOptions = {}) {
    this.onListenerError = options.onListenerError;
  }

  createNode(parent: NodeId | null = null): NodeId {
    if (parent !== null) this.requireNode(parent);
    const id = this.nextId++;
    this.parents.set(id, parent);
    return id;
  }

  hasNode(id: NodeId): boolean {
    return this.parents.has(id);
  }

  getParent(id: NodeId): NodeId | null {
    this.requireNode(id);
    return this.parents.get(id) ?? null;
  }

  setParent(id: NodeId, parent: NodeId | null): void {
    this.requireNode(id);
    if (parent !== null) this.requireNode(parent);
    if (id === parent) throw new Error("a node cannot be its own parent");
    for (let cur: NodeId | null = parent; cur !== null; ) {
      if (cur === id) throw new Error("setParent would create a cycle");
      cur = this.parents.get(cur) ?? null;
    }
    this.parents.set(id, parent);
  }

  removeNode(id: NodeId): void {
    this.requireNode(id);
    const parent = this.parents.get(id) ?? null;
    for (const [child, p] of this.parents) {
      if (p === id) this.parents.set(child, parent);
    }
    this.parents.delete(id);
  }

  addEventListener<T>(
    id: NodeId,
    type: string,
    listener: Listener<T>,
    options: AddListenerOptions = {},
  ): void {
    this.requireNode(id);
    const capture = options.capture ?? false;
    const once = options.once ?? false;
    let byType = this.listeners.get(id);
    if (!byType) {
      byType = new Map();
      this.listeners.set(id, byType);
    }
    let list = byType.get(type);
    if (!list) {
      list = [];
      byType.set(type, list);
    }
    const dup = list.some(
      (rec) => rec.listener === (listener as Listener<never>) && rec.capture === capture,
    );
    if (dup) return;
    list.push({ listener: listener as Listener<never>, capture, once });
  }

  removeEventListener<T>(
    id: NodeId,
    type: string,
    listener: Listener<T>,
    options: RemoveListenerOptions = {},
  ): void {
    const capture = options.capture ?? false;
    const list = this.listeners.get(id)?.get(type);
    if (!list) return;
    for (let i = list.length - 1; i >= 0; i--) {
      if (list[i].listener === (listener as Listener<never>) && list[i].capture === capture) {
        list.splice(i, 1);
      }
    }
  }

  hasListener(id: NodeId, type: string): boolean {
    return (this.listeners.get(id)?.get(type)?.length ?? 0) > 0;
  }

  dispatchEvent<T>(target: NodeId, event: TreeEvent<T> | TreeEventInit<T>): TreeEvent<T> {
    this.requireNode(target);
    const ev = event instanceof TreeEvent ? (event as TreeEvent<T>) : new TreeEvent<T>(event);
    const path = this.pathTo(target);
    ev.target = target;
    const errors: ListenerErrorInfo[] = [];

    const runNode = (node: NodeId, phase: Phase, atTarget: boolean): boolean => {
      const live = this.listeners.get(node)?.get(ev.type);
      if (!live || live.length === 0) return false;
      const snapshot = live.slice();
      for (const rec of snapshot) {
        if (!atTarget) {
          if (phase === "capture" && !rec.capture) continue;
          if (phase === "bubble" && rec.capture) continue;
        }
        const current = this.listeners.get(node)?.get(ev.type);
        if (!current || !current.includes(rec)) continue;
        if (rec.once) current.splice(current.indexOf(rec), 1);
        ev.currentTarget = node;
        ev.phase = phase;
        try {
          rec.listener(ev as TreeEvent<never>);
        } catch (error) {
          const info: ListenerErrorInfo = { node, type: ev.type, phase, error };
          errors.push(info);
          this.onListenerError?.(info);
        }
        if (ev.immediatePropagationStopped) return true;
      }
      return false;
    };

    dispatch: {
      for (let i = 0; i < path.length - 1; i++) {
        if (runNode(path[i], "capture", false)) break dispatch;
        if (ev.propagationStopped) break dispatch;
      }
      if (runNode(target, "target", true)) break dispatch;
      if (ev.propagationStopped) break dispatch;
      if (ev.bubbles) {
        for (let i = path.length - 2; i >= 0; i--) {
          if (runNode(path[i], "bubble", false)) break dispatch;
          if (ev.propagationStopped) break dispatch;
        }
      }
    }

    ev.currentTarget = null;
    ev.phase = "none";

    if (errors.length > 0 && !this.onListenerError) {
      throw new AggregateError(
        errors.map((e) => e.error),
        `${errors.length} listener(s) threw during dispatch of "${ev.type}"`,
      );
    }
    return ev;
  }

  private pathTo(id: NodeId): NodeId[] {
    const path: NodeId[] = [];
    const seen = new Set<NodeId>();
    let cur: NodeId | null = id;
    while (cur !== null) {
      if (seen.has(cur)) throw new Error("cycle detected in tree");
      seen.add(cur);
      path.push(cur);
      cur = this.parents.get(cur) ?? null;
    }
    return path.reverse();
  }

  private requireNode(id: NodeId): void {
    if (!this.parents.has(id)) throw new Error(`unknown node: ${id}`);
  }
}
