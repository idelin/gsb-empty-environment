/**
 * 导航状态包：为浏览器测试宿主建模嵌套文档的隔离域。
 *
 * - 限制分为三项：脚本(scripts)、弹窗(popups)、保留来源(preserveOrigin)，
 *   生效限制 = 父文档生效限制 ∪ 本层声明。
 * - 声明的修改仅在下一次导航时生效。
 * - 生效限制禁止保留来源时，分配不可与其他文档相等的不透明来源。
 * - 派生弹窗继承创建时刻的限制快照。
 * - 解除挂载使旧导航回调失效；文档号可回收复用，由代次(generation)区分。
 */

export interface Restrictions {
  readonly scripts: boolean;
  readonly popups: boolean;
  readonly preserveOrigin: boolean;
}

export const NO_RESTRICTIONS: Restrictions = Object.freeze({
  scripts: false,
  popups: false,
  preserveOrigin: false,
});

export function unionRestrictions(a: Restrictions, b: Restrictions): Restrictions {
  return {
    scripts: a.scripts || b.scripts,
    popups: a.popups || b.popups,
    preserveOrigin: a.preserveOrigin || b.preserveOrigin,
  };
}

export function restrictionsEqual(a: Restrictions, b: Restrictions): boolean {
  return (
    a.scripts === b.scripts &&
    a.popups === b.popups &&
    a.preserveOrigin === b.preserveOrigin
  );
}

export type Origin =
  | { readonly kind: 'url'; readonly value: string }
  | { readonly kind: 'opaque'; readonly nonce: number };

/**
 * 不透明来源仅在持有同一令牌时相等；nonce 由宿主单调分配，
 * 因此任何两个文档分到的不透明来源互不相等。
 */
export function sameOrigin(a: Origin, b: Origin): boolean {
  if (a.kind === 'url' && b.kind === 'url') return a.value === b.value;
  if (a.kind === 'opaque' && b.kind === 'opaque') return a.nonce === b.nonce;
  return false;
}

/** 文档身份：文档号可复用，代次随每次挂载递增。 */
export interface DocumentRef {
  readonly number: number;
  readonly generation: number;
}

export type NavEvent =
  | { readonly type: 'created'; readonly doc: DocumentRef; readonly parent: DocumentRef | null }
  | { readonly type: 'declared'; readonly doc: DocumentRef; readonly pending: Restrictions }
  | {
      readonly type: 'navigated';
      readonly doc: DocumentRef;
      readonly navigation: number;
      readonly effective: Restrictions;
      readonly origin: Origin;
    }
  | {
      readonly type: 'popup-opened';
      readonly opener: DocumentRef;
      readonly popup: DocumentRef;
      readonly inherited: Restrictions;
    }
  | { readonly type: 'callback'; readonly doc: DocumentRef; readonly navigation: number }
  | {
      readonly type: 'callback-dropped';
      readonly doc: DocumentRef;
      readonly navigation: number;
      readonly reason: 'unmounted' | 'superseded';
    }
  | { readonly type: 'unmounted'; readonly doc: DocumentRef };

export class StaleDocumentError extends Error {
  readonly ref: DocumentRef;
  constructor(ref: DocumentRef) {
    super(`document #${ref.number} (generation ${ref.generation}) is no longer attached`);
    this.name = 'StaleDocumentError';
    this.ref = ref;
  }
}

export class PopupBlockedError extends Error {
  readonly ref: DocumentRef;
  constructor(ref: DocumentRef) {
    super(`document #${ref.number} is restricted from opening popups`);
    this.name = 'PopupBlockedError';
    this.ref = ref;
  }
}

export interface DocumentSnapshot {
  readonly effective: Restrictions;
  readonly origin: Origin;
  readonly navigation: number;
}

interface DocumentState {
  readonly ref: DocumentRef;
  readonly base: Restrictions;
  declared: Restrictions;
  effective: Restrictions;
  origin: Origin;
  navigation: number;
  alive: boolean;
}

interface QueuedCallback {
  readonly ref: DocumentRef;
  readonly navigation: number;
  readonly run: () => void;
}

interface Slot {
  generation: number;
  free: boolean;
}

export class NavigationHost {
  readonly events: NavEvent[] = [];
  #slots: Slot[] = [];
  #documents = new Map<number, DocumentState>();
  #queue: QueuedCallback[] = [];
  #nextOpaqueNonce = 1;

  createRoot(url?: string): DocumentHandle {
    return this.#attach(NO_RESTRICTIONS, null, url, null);
  }

  createDocument(parent: DocumentHandle, url?: string): DocumentHandle {
    const parentState = this.#resolve(parent.ref);
    return this.#attach(parentState.effective, parentState.ref, url, parentState.origin);
  }

  openPopup(opener: DocumentHandle): DocumentHandle {
    const openerState = this.#resolve(opener.ref);
    if (openerState.effective.popups) throw new PopupBlockedError(opener.ref);
    const handle = this.#attach(
      openerState.effective,
      openerState.ref,
      undefined,
      openerState.origin,
    );
    this.#emit({
      type: 'popup-opened',
      opener: openerState.ref,
      popup: handle.ref,
      inherited: openerState.effective,
    });
    return handle;
  }

  declareRestrictions(ref: DocumentRef, patch: Partial<Restrictions>): void {
    const state = this.#resolve(ref);
    state.declared = { ...state.declared, ...patch };
    this.#emit({ type: 'declared', doc: ref, pending: state.declared });
  }

  navigate(ref: DocumentRef, url?: string, callback?: () => void): void {
    const state = this.#resolve(ref);
    state.effective = unionRestrictions(state.base, state.declared);
    if (state.effective.preserveOrigin) {
      state.origin = this.#freshOpaqueOrigin();
    } else if (url !== undefined) {
      state.origin = { kind: 'url', value: new URL(url).origin };
    }
    state.navigation += 1;
    this.#emit({
      type: 'navigated',
      doc: ref,
      navigation: state.navigation,
      effective: state.effective,
      origin: state.origin,
    });
    if (callback !== undefined) {
      this.#queue.push({ ref, navigation: state.navigation, run: callback });
    }
  }

  unmount(ref: DocumentRef): void {
    const state = this.#resolve(ref);
    state.alive = false;
    this.#documents.delete(ref.number);
    this.#slots[ref.number].free = true;
    this.#emit({ type: 'unmounted', doc: ref });
  }

  /** 投递排队的导航回调；已失效（卸载/被更新导航取代）的回调被丢弃。 */
  drain(): void {
    const queue = this.#queue.splice(0);
    for (const item of queue) {
      const state = this.#documents.get(item.ref.number);
      const attached =
        state !== undefined && state.alive && state.ref.generation === item.ref.generation;
      if (attached && state.navigation === item.navigation) {
        this.#emit({ type: 'callback', doc: item.ref, navigation: item.navigation });
        item.run();
      } else {
        this.#emit({
          type: 'callback-dropped',
          doc: item.ref,
          navigation: item.navigation,
          reason: attached ? 'superseded' : 'unmounted',
        });
      }
    }
  }

  snapshot(ref: DocumentRef): DocumentSnapshot {
    const state = this.#resolve(ref);
    return {
      effective: state.effective,
      origin: state.origin,
      navigation: state.navigation,
    };
  }

  #attach(
    base: Restrictions,
    parent: DocumentRef | null,
    url: string | undefined,
    fallbackOrigin: Origin | null,
  ): DocumentHandle {
    const ref = this.#allocate();
    let origin: Origin;
    if (base.preserveOrigin) {
      origin = this.#freshOpaqueOrigin();
    } else if (url !== undefined) {
      origin = { kind: 'url', value: new URL(url).origin };
    } else if (fallbackOrigin !== null) {
      origin = fallbackOrigin;
    } else {
      origin = { kind: 'url', value: 'about:blank' };
    }
    const state: DocumentState = {
      ref,
      base,
      declared: { ...NO_RESTRICTIONS },
      effective: base,
      origin,
      navigation: 0,
      alive: true,
    };
    this.#documents.set(ref.number, state);
    this.#emit({ type: 'created', doc: ref, parent });
    return new DocumentHandle(this, ref);
  }

  #allocate(): DocumentRef {
    for (let number = 0; number < this.#slots.length; number += 1) {
      const slot = this.#slots[number];
      if (slot.free) {
        slot.free = false;
        slot.generation += 1;
        return { number, generation: slot.generation };
      }
    }
    this.#slots.push({ generation: 0, free: false });
    return { number: this.#slots.length - 1, generation: 0 };
  }

  #resolve(ref: DocumentRef): DocumentState {
    const state = this.#documents.get(ref.number);
    if (state === undefined || !state.alive || state.ref.generation !== ref.generation) {
      throw new StaleDocumentError(ref);
    }
    return state;
  }

  #freshOpaqueOrigin(): Origin {
    const nonce = this.#nextOpaqueNonce;
    this.#nextOpaqueNonce += 1;
    return { kind: 'opaque', nonce };
  }

  #emit(event: NavEvent): void {
    this.events.push(event);
  }
}

export class DocumentHandle {
  readonly #host: NavigationHost;
  readonly ref: DocumentRef;

  constructor(host: NavigationHost, ref: DocumentRef) {
    this.#host = host;
    this.ref = ref;
  }

  get number(): number {
    return this.ref.number;
  }

  get generation(): number {
    return this.ref.generation;
  }

  /** 修改本层声明；仅在下一次导航时并入生效限制。 */
  declareRestrictions(patch: Partial<Restrictions>): void {
    this.#host.declareRestrictions(this.ref, patch);
  }

  navigate(url?: string, callback?: () => void): void {
    this.#host.navigate(this.ref, url, callback);
  }

  unmount(): void {
    this.#host.unmount(this.ref);
  }

  createChild(url?: string): DocumentHandle {
    return this.#host.createDocument(this, url);
  }

  openPopup(): DocumentHandle {
    return this.#host.openPopup(this);
  }

  snapshot(): DocumentSnapshot {
    return this.#host.snapshot(this.ref);
  }
}
