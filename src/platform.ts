/**
 * Simulated messaging platform.
 *
 * Models the browser pieces that matter for the navigation race:
 *  - A `SimulatedWindow` plays the role of the WindowProxy: the host can hold
 *    a stable reference to it while the document inside is replaced by
 *    navigation. Each document gets a fresh, unguessable `generation` token.
 *  - `postMessage` stamps every message *at send time* with trusted metadata
 *    (origin + document generation of the sender). The metadata is frozen and
 *    carried inside a `PlatformMessageEvent` private field, so page code can
 *    read it but neither forge nor mutate it.
 *  - Delivery goes through an explicit, controllable event loop so tests can
 *    interleave receive / validate / reply with navigation precisely.
 */

export type DocumentGeneration = { readonly id: number };

let nextGenerationId = 0;

export class SimulatedDocument {
  readonly url: string;
  readonly origin: string;
  readonly generation: DocumentGeneration;

  constructor(url: string) {
    this.url = url;
    this.origin = new URL(url).origin;
    this.generation = Object.freeze({ id: ++nextGenerationId });
    Object.freeze(this);
  }
}

export interface TrustedMessageMeta {
  readonly origin: string;
  readonly generation: DocumentGeneration;
  readonly document: SimulatedDocument;
}

export class PlatformMessageEvent {
  readonly data: unknown;
  readonly source: SimulatedWindow;
  readonly origin: string;
  readonly #meta: TrustedMessageMeta;

  constructor(data: unknown, source: SimulatedWindow, meta: TrustedMessageMeta) {
    this.data = data;
    this.source = source;
    this.origin = meta.origin;
    this.#meta = meta;
    Object.freeze(this);
  }

  /** Platform-stamped metadata captured when the message was sent. */
  get trustedMeta(): TrustedMessageMeta {
    return this.#meta;
  }
}

export type MessageListener = (event: PlatformMessageEvent) => void;
export type NavigateListener = (document: SimulatedDocument) => void;

export class SimulatedWindow {
  readonly name: string;
  #document: SimulatedDocument;
  #messageListeners: Set<MessageListener> = new Set();
  #navigateListeners: Set<NavigateListener> = new Set();

  constructor(name: string, url: string) {
    this.name = name;
    this.#document = new SimulatedDocument(url);
  }

  get document(): SimulatedDocument {
    return this.#document;
  }

  get currentOrigin(): string {
    return this.#document.origin;
  }

  get currentGeneration(): DocumentGeneration {
    return this.#document.generation;
  }

  addMessageListener(listener: MessageListener): void {
    this.#messageListeners.add(listener);
  }

  removeMessageListener(listener: MessageListener): void {
    this.#messageListeners.delete(listener);
  }

  onNavigate(listener: NavigateListener): void {
    this.#navigateListeners.add(listener);
  }

  /**
   * Replaces the current document with a new one. The window object itself is
   * reused (like a WindowProxy), but the document generation changes and all
   * message listeners registered by the old document are gone.
   */
  navigate(url: string): SimulatedDocument {
    this.#document = new SimulatedDocument(url);
    this.#messageListeners = new Set();
    for (const listener of this.#navigateListeners) listener(this.#document);
    return this.#document;
  }

  /** Platform-internal: dispatch an event to the current document. */
  dispatchMessage(event: PlatformMessageEvent): void {
    for (const listener of [...this.#messageListeners]) listener(event);
  }
}

export type Task = () => void;

export class EventLoop {
  #queue: Array<Task> = [];

  postTask(task: Task): void {
    this.#queue.push(task);
  }

  get pendingCount(): number {
    return this.#queue.length;
  }

  runNextTask(): boolean {
    const task = this.#queue.shift();
    if (task === undefined) return false;
    task();
    return true;
  }

  flush(): void {
    while (this.runNextTask()) {
      // drain
    }
  }
}

export class Platform {
  readonly eventLoop: EventLoop = new EventLoop();
  #genuineEvents: WeakSet<PlatformMessageEvent> = new WeakSet();

  createWindow(name: string, url: string): SimulatedWindow {
    return new SimulatedWindow(name, url);
  }

  /** True only for events this platform actually stamped and dispatched. */
  isGenuineMessage(event: PlatformMessageEvent): boolean {
    return this.#genuineEvents.has(event);
  }

  /**
   * Regular postMessage: trusted metadata is captured now (send time); the
   * message is delivered to whatever document is current when the task runs,
   * provided its origin matches targetOrigin.
   */
  postMessage(source: SimulatedWindow, target: SimulatedWindow, data: unknown, targetOrigin: string): void {
    const meta = this.#stamp(source);
    this.eventLoop.postTask(() => {
      if (!this.#originMatches(target, targetOrigin)) return;
      this.#dispatch(target, new PlatformMessageEvent(data, source, meta));
    });
  }

  /**
   * Document-bound delivery used by reply capabilities: the message is only
   * delivered if the target window still hosts the exact document generation
   * the capability was issued for. The generation is checked both when the
   * reply is queued and again when the delivery task runs, closing the
   * time-of-check/time-of-use gap across event-loop turns.
   *
   * Returns false immediately if the generation is already stale.
   */
  postMessageToDocument(
    source: SimulatedWindow,
    target: SimulatedWindow,
    data: unknown,
    targetOrigin: string,
    expectedGeneration: DocumentGeneration,
  ): boolean {
    if (target.currentGeneration !== expectedGeneration) return false;
    const meta = this.#stamp(source);
    this.eventLoop.postTask(() => {
      if (target.currentGeneration !== expectedGeneration) return;
      if (!this.#originMatches(target, targetOrigin)) return;
      this.#dispatch(target, new PlatformMessageEvent(data, source, meta));
    });
    return true;
  }

  #dispatch(target: SimulatedWindow, event: PlatformMessageEvent): void {
    this.#genuineEvents.add(event);
    target.dispatchMessage(event);
  }

  #stamp(source: SimulatedWindow): TrustedMessageMeta {
    return Object.freeze({
      origin: source.currentOrigin,
      generation: source.currentGeneration,
      document: source.document,
    });
  }

  #originMatches(target: SimulatedWindow, targetOrigin: string): boolean {
    return targetOrigin === '*' || target.currentOrigin === targetOrigin;
  }
}
