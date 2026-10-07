/**
 * tree-events: DOM-style event dispatch (capture / target / bubble) over a
 * virtual tree of EventNode objects.
 *
 * Semantics:
 * - The propagation path is snapshotted when dispatch begins; adding or
 *   removing nodes during dispatch does not change the current path.
 * - A node's listener list is snapshotted when that node is invoked, but
 *   removals take effect immediately: a listener removed before its turn
 *   (even from within another listener on the same node) is not called.
 * - `once` listeners are removed before they are invoked, so a re-entrant
 *   dispatch from inside the listener will not re-trigger it.
 * - `stopPropagation()` prevents later nodes from being invoked but lets the
 *   current node's remaining listeners run; `stopImmediatePropagation()`
 *   also stops the current node's remaining listeners.
 * - Listener exceptions are caught, collected, and dispatch continues. They
 *   are returned in `DispatchResult.errors`.
 */

export const PHASE_NONE = 0;
export const PHASE_CAPTURING = 1;
export const PHASE_AT_TARGET = 2;
export const PHASE_BUBBLING = 3;

export type EventPhase =
  | typeof PHASE_NONE
  | typeof PHASE_CAPTURING
  | typeof PHASE_AT_TARGET
  | typeof PHASE_BUBBLING;

export interface TreeEventInit {
  bubbles?: boolean;
  cancelable?: boolean;
  detail?: unknown;
}

export class TreeEvent {
  readonly type: string;
  readonly bubbles: boolean;
  readonly cancelable: boolean;
  readonly detail: unknown;

  target: EventNode | null = null;
  currentTarget: EventNode | null = null;
  eventPhase: EventPhase = PHASE_NONE;
  defaultPrevented = false;

  /** Internal propagation flags, read by the dispatcher. */
  propagationStopped = false;
  immediatePropagationStopped = false;
  /** True while this event object is being dispatched. */
  dispatching = false;

  constructor(type: string, init: TreeEventInit = {}) {
    this.type = type;
    this.bubbles = init.bubbles ?? true;
    this.cancelable = init.cancelable ?? false;
    this.detail = init.detail;
  }

  stopPropagation(): void {
    this.propagationStopped = true;
  }

  stopImmediatePropagation(): void {
    this.immediatePropagationStopped = true;
    this.propagationStopped = true;
  }

  preventDefault(): void {
    if (this.cancelable) this.defaultPrevented = true;
  }
}

export type Listener<E extends TreeEvent = TreeEvent> = (event: E) => void;

export interface AddListenerOptions {
  capture?: boolean;
  once?: boolean;
}

export interface RemoveListenerOptions {
  capture?: boolean;
}

export interface DispatchResult {
  /** True if a cancelable event had preventDefault() called on it. */
  canceled: boolean;
  /** Errors thrown by listeners, in the order they were thrown. */
  errors: unknown[];
}

interface ListenerEntry {
  listener: Listener;
  capture: boolean;
  once: boolean;
  removed: boolean;
}

function normalizeOptions(
  options: boolean | AddListenerOptions | undefined,
): { capture: boolean; once: boolean } {
  if (typeof options === "boolean") return { capture: options, once: false };
  return { capture: options?.capture ?? false, once: options?.once ?? false };
}

export class EventNode {
  /** Optional name, handy for debugging and call logs. */
  name: string | undefined;

  private _parent: EventNode | null = null;
  private readonly _children = new Set<EventNode>();
  private readonly _listeners = new Map<string, ListenerEntry[]>();

  constructor(name?: string) {
    this.name = name;
  }

  get parent(): EventNode | null {
    return this._parent;
  }

  get children(): readonly EventNode[] {
    return [...this._children];
  }

  appendChild(child: EventNode): void {
    if (child === this) throw new Error("cannot parent a node to itself");
    for (let n: EventNode | null = this; n; n = n._parent) {
      if (n === child) throw new Error("cycle detected in event tree");
    }
    child.detach();
    child._parent = this;
    this._children.add(child);
  }

  removeChild(child: EventNode): void {
    if (this._children.delete(child)) child._parent = null;
  }

  /** Detach this node from its parent. */
  detach(): void {
    if (this._parent) this._parent.removeChild(this);
  }

  addEventListener(
    type: string,
    listener: Listener,
    options?: boolean | AddListenerOptions,
  ): void {
    const { capture, once } = normalizeOptions(options);
    let list = this._listeners.get(type);
    if (!list) {
      list = [];
      this._listeners.set(type, list);
    }
    // DOM-style dedup: same listener + same capture flag registers once.
    for (const entry of list) {
      if (!entry.removed && entry.listener === listener && entry.capture === capture) {
        return;
      }
    }
    list.push({ listener, capture, once, removed: false });
  }

  removeEventListener(
    type: string,
    listener: Listener,
    options?: boolean | RemoveListenerOptions,
  ): void {
    const capture = typeof options === "boolean" ? options : options?.capture ?? false;
    const list = this._listeners.get(type);
    if (!list) return;
    for (const entry of list) {
      if (!entry.removed && entry.listener === listener && entry.capture === capture) {
        // Flag as removed so in-flight dispatches skip it immediately,
        // then drop it from the live list.
        entry.removed = true;
        list.splice(list.indexOf(entry), 1);
        return;
      }
    }
  }

  hasEventListener(type: string, listener?: Listener): boolean {
    const list = this._listeners.get(type);
    if (!list) return false;
    if (!listener) return list.some((e) => !e.removed);
    return list.some((e) => !e.removed && e.listener === listener);
  }

  dispatchEvent(event: TreeEvent): DispatchResult {
    if (event.dispatching) {
      throw new Error(`event "${event.type}" is already being dispatched`);
    }
    event.dispatching = true;
    event.target = this;
    event.propagationStopped = false;
    event.immediatePropagationStopped = false;

    const errors: unknown[] = [];

    // Snapshot the propagation path (ancestors, parent first). Tree
    // mutations during dispatch do not affect this dispatch.
    const ancestors: EventNode[] = [];
    for (let n = this._parent; n; n = n._parent) ancestors.push(n);

    try {
      // Capture: root -> parent.
      for (let i = ancestors.length - 1; i >= 0; i--) {
        if (event.propagationStopped) break;
        ancestors[i]!.invokeListeners(event, PHASE_CAPTURING, errors);
      }
      // Target.
      if (!event.propagationStopped) {
        this.invokeListeners(event, PHASE_AT_TARGET, errors);
      }
      // Bubble: parent -> root.
      if (event.bubbles && !event.propagationStopped) {
        for (const node of ancestors) {
          if (event.propagationStopped) break;
          node.invokeListeners(event, PHASE_BUBBLING, errors);
        }
      }
    } finally {
      event.eventPhase = PHASE_NONE;
      event.currentTarget = null;
      event.dispatching = false;
    }

    return { canceled: event.defaultPrevented, errors };
  }

  private invokeListeners(
    event: TreeEvent,
    phase: EventPhase,
    errors: unknown[],
  ): void {
    const list = this._listeners.get(event.type);
    if (!list || list.length === 0) return;

    event.currentTarget = this;
    event.eventPhase = phase;

    // Snapshot the list as it is when this node starts invoking. Listeners
    // added afterwards do not run in this dispatch; entries removed before
    // their turn are skipped via the `removed` flag.
    const snapshot = list.slice();
    const atTarget = phase === PHASE_AT_TARGET;

    for (const entry of snapshot) {
      if (event.immediatePropagationStopped) break;
      if (entry.removed) continue;
      // During capture/bubble only matching-direction listeners run; at the
      // target both kinds run in registration order.
      if (!atTarget) {
        if (phase === PHASE_CAPTURING && !entry.capture) continue;
        if (phase === PHASE_BUBBLING && entry.capture) continue;
      }
      // `once` listeners are removed before invocation.
      if (entry.once) {
        entry.removed = true;
        const idx = list.indexOf(entry);
        if (idx !== -1) list.splice(idx, 1);
      }
      try {
        entry.listener.call(this, event);
      } catch (err) {
        errors.push(err);
      }
    }
  }
}
