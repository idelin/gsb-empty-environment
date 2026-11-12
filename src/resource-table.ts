export type ResourceType = string;

export type HandleRetireReason = "dropped" | "moved" | "borrow-ended";

export type HandleErrorCode =
  | "HANDLE_INVALID"
  | "HANDLE_STALE"
  | "HANDLE_DROPPED"
  | "HANDLE_MOVED"
  | "HANDLE_BORROW_EXPIRED"
  | "HANDLE_TYPE_MISMATCH"
  | "HANDLE_KIND_MISMATCH"
  | "HANDLE_IN_USE"
  | "CALL_SCOPE_INVALID"
  | "CALL_SCOPE_CLOSED";

export class HandleError extends Error {
  readonly code: HandleErrorCode;
  readonly handle: ResourceHandle | undefined;
  readonly expectedType: ResourceType | undefined;
  readonly actualType: ResourceType | undefined;

  constructor(
    code: HandleErrorCode,
    message: string,
    details: {
      handle?: ResourceHandle;
      expectedType?: ResourceType;
      actualType?: ResourceType;
    } = {},
  ) {
    super(message);
    this.name = "HandleError";
    this.code = code;
    this.handle = details.handle;
    this.expectedType = details.expectedType;
    this.actualType = details.actualType;
  }
}

/**
 * Opaque capability token. A component may pass this object back across the
 * boundary, but it must not be able to mint one that names another table or
 * an arbitrary slot generation.
 */
export interface ResourceHandle {
  readonly index: number;
  readonly generation: number;
}

type SlotState = "free" | "own" | "borrow";

interface HandleMetadata {
  readonly owner: object;
}

const HANDLE_METADATA = new WeakMap<object, HandleMetadata>();

class HandleToken implements ResourceHandle {
  readonly #index: number;
  readonly #generation: number;

  constructor(index: number, generation: number, owner: object) {
    this.#index = index;
    this.#generation = generation;
    HANDLE_METADATA.set(this, { owner });
    Object.freeze(this);
  }

  get index(): number {
    return this.#index;
  }

  get generation(): number {
    return this.#generation;
  }
}

Object.defineProperty(HandleToken.prototype, "constructor", {
  value: undefined,
  configurable: false,
  writable: false,
});

function readHandle(
  value: unknown,
): { index: number; generation: number; owner: object } | null {
  if (value === null || typeof value !== "object") {
    return null;
  }
  const metadata = HANDLE_METADATA.get(value);
  if (metadata === undefined) {
    return null;
  }
  try {
    const token = value as HandleToken;
    const index = token.index;
    const generation = token.generation;
    if (
      !Number.isSafeInteger(index) ||
      index < 0 ||
      !Number.isSafeInteger(generation) ||
      generation < 0
    ) {
      return null;
    }
    return { index, generation, owner: metadata.owner };
  } catch {
    return null;
  }
}

interface Slot {
  type: ResourceType;
  value: unknown;
  generation: number;
  state: SlotState;
  scope: CallScope | null;
  rootIndex: number;
  rootGeneration: number;
  borrowRefs: number;
  retiredGeneration: number | null;
  retiredReason: HandleRetireReason | null;
}

function makeSlot(): Slot {
  return {
    type: "",
    value: undefined,
    generation: 0,
    state: "free",
    scope: null,
    rootIndex: -1,
    rootGeneration: -1,
    borrowRefs: 0,
    retiredGeneration: null,
    retiredReason: null,
  };
}

function assertResourceType(type: ResourceType): void {
  if (typeof type !== "string" || type.trim().length === 0) {
    throw new TypeError("resource type must be a non-empty string");
  }
}

export class CallScope {
  readonly id: number;
  readonly table: ResourceTable;

  /** @internal Use ResourceTable.beginCall() to create a scope. */
  constructor(table: ResourceTable, id: number) {
    this.table = table;
    this.id = id;
    CALL_SCOPE_STATE.set(this, { active: true, borrows: [] });
  }

  get active(): boolean {
    return scopeState(this).active;
  }

  borrow(source: ResourceHandle, expectedType: ResourceType): ResourceHandle {
    return this.table.borrow(source, expectedType, this);
  }
}

interface CallScopeState {
  active: boolean;
  borrows: ResourceHandle[];
}

const CALL_SCOPE_STATE = new WeakMap<object, CallScopeState>();

function scopeState(scope: CallScope): CallScopeState {
  const state = CALL_SCOPE_STATE.get(scope);
  if (state === undefined) {
    throw new HandleError(
      "CALL_SCOPE_INVALID",
      "call scope was not issued by a resource table",
    );
  }
  return state;
}

export type HandleUseKind = "own" | "borrow" | "any";

export class ResourceTable {
  readonly #owner = Object.freeze({});
  #slots: Slot[] = [];
  #freeIndices: number[] = [];
  #nextScopeId = 0;

  /** @internal */
  owns(handle: ResourceHandle): boolean {
    return readHandle(handle)?.owner === this.#owner;
  }

  createOwn(type: ResourceType, value: unknown): ResourceHandle {
    return this.#allocate(type, value, "own", null, -1, -1);
  }

  /** Alias used by callers that model the host-to-guest boundary explicitly. */
  lift(type: ResourceType, value: unknown): ResourceHandle {
    return this.createOwn(type, value);
  }

  beginCall(): CallScope {
    return new CallScope(this, ++this.#nextScopeId);
  }

  endCall(scope: CallScope): void {
    if (!(scope instanceof CallScope) || scope.table !== this) {
      throw new HandleError(
        "CALL_SCOPE_INVALID",
        "call scope does not belong to this resource table",
      );
    }
    const state = scopeState(scope);
    if (!state.active) {
      return;
    }

    state.active = false;
    const borrows = state.borrows;
    state.borrows = [];
    for (const borrow of borrows) {
      const slot = this.#slots[borrow.index];
      if (
        slot === undefined ||
        slot.generation !== borrow.generation ||
        slot.state !== "borrow" ||
        slot.scope !== scope
      ) {
        continue;
      }

      this.#releaseBorrowRoot(slot);
      this.#retireSlot(borrow.index, "borrow-ended");
    }
  }

  withCall<T>(body: (scope: CallScope) => T): T {
    const scope = this.beginCall();
    try {
      return body(scope);
    } finally {
      this.endCall(scope);
    }
  }

  async withCallAsync<T>(
    body: (scope: CallScope) => T | PromiseLike<T>,
  ): Promise<T> {
    const scope = this.beginCall();
    try {
      return await body(scope);
    } finally {
      this.endCall(scope);
    }
  }

  borrow(
    source: ResourceHandle,
    expectedType: ResourceType,
    scope: CallScope,
  ): ResourceHandle {
    assertResourceType(expectedType);
    if (!(scope instanceof CallScope) || scope.table !== this) {
      throw new HandleError(
        "CALL_SCOPE_INVALID",
        "call scope does not belong to this resource table",
      );
    }
    const state = scopeState(scope);
    if (!state.active) {
      throw new HandleError(
        "CALL_SCOPE_CLOSED",
        "borrow handles cannot be created after the call has returned",
      );
    }

    const resolved = this.#resolve(source, expectedType, "any");
    const rootIndex =
      resolved.slot.state === "own" ? resolved.index : resolved.slot.rootIndex;
    const rootGeneration =
      resolved.slot.state === "own"
        ? resolved.slot.generation
        : resolved.slot.rootGeneration;
    const root = this.#slots[rootIndex];
    if (
      root === undefined ||
      root.generation !== rootGeneration ||
      root.state !== "own"
    ) {
      throw new HandleError(
        "HANDLE_INVALID",
        "borrow source does not have a live owning resource",
        { handle: source, expectedType },
      );
    }

    const handle = this.#allocate(
      expectedType,
      resolved.slot.value,
      "borrow",
      scope,
      rootIndex,
      rootGeneration,
    );
    root.borrowRefs += 1;
    state.borrows.push(handle);
    return handle;
  }

  /**
   * Consume an own handle and create a fresh own handle in the destination
   * table. The source handle is immediately retired, so a component cannot
   * keep using it after ownership was transferred.
   */
  transferOwn(
    source: ResourceHandle,
    expectedType: ResourceType,
    target: ResourceTable = this,
  ): ResourceHandle {
    assertResourceType(expectedType);
    if (!(target instanceof ResourceTable)) {
      throw new TypeError("transfer destination must be a ResourceTable");
    }

    const resolved = this.#resolve(source, expectedType, "own");
    if (resolved.slot.borrowRefs > 0) {
      throw new HandleError(
        "HANDLE_IN_USE",
        "cannot transfer an own handle while a live borrow references it",
        { handle: source, expectedType },
      );
    }

    const destination = target.#allocate(
      resolved.slot.type,
      resolved.slot.value,
      "own",
      null,
      -1,
      -1,
    );
    this.#retireSlot(resolved.index, "moved");
    return destination;
  }

  use(
    handle: ResourceHandle,
    expectedType: ResourceType,
    expectedKind: HandleUseKind = "any",
  ): unknown {
    assertResourceType(expectedType);
    return this.#resolve(handle, expectedType, expectedKind).slot.value;
  }

  useOwn(handle: ResourceHandle, expectedType: ResourceType): unknown {
    return this.use(handle, expectedType, "own");
  }

  useBorrow(handle: ResourceHandle, expectedType: ResourceType): unknown {
    return this.use(handle, expectedType, "borrow");
  }

  drop(handle: ResourceHandle, expectedType: ResourceType): void {
    assertResourceType(expectedType);
    const resolved = this.#resolve(handle, expectedType, "own");
    if (resolved.slot.borrowRefs > 0) {
      throw new HandleError(
        "HANDLE_IN_USE",
        "cannot drop an own handle while a live borrow references it",
        { handle, expectedType },
      );
    }
    this.#retireSlot(resolved.index, "dropped");
  }

  inspect(handle: ResourceHandle): {
    index: number;
    generation: number;
    type: ResourceType;
    kind: "own" | "borrow";
    callScopeId: number | null;
  } {
    const resolved = this.#resolve(handle, undefined, "any");
    return {
      index: resolved.index,
      generation: resolved.slot.generation,
      type: resolved.slot.type,
      kind: resolved.slot.state as "own" | "borrow",
      callScopeId: resolved.slot.scope?.id ?? null,
    };
  }

  get slotCount(): number {
    return this.#slots.length;
  }

  get activeSlotCount(): number {
    let count = 0;
    for (const slot of this.#slots) {
      if (slot.state !== "free") {
        count += 1;
      }
    }
    return count;
  }

  #allocate(
    type: ResourceType,
    value: unknown,
    state: Exclude<SlotState, "free">,
    scope: CallScope | null,
    rootIndex: number,
    rootGeneration: number,
  ): ResourceHandle {
    assertResourceType(type);

    let index = this.#freeIndices.pop();
    if (index === undefined) {
      index = this.#slots.length;
      this.#slots.push(makeSlot());
    }

    const slot = this.#slots[index]!;
    slot.type = type;
    slot.value = value;
    slot.state = state;
    slot.scope = scope;
    slot.rootIndex = rootIndex;
    slot.rootGeneration = rootGeneration;
    slot.borrowRefs = 0;

    return new HandleToken(index, slot.generation, this.#owner);
  }

  #retireSlot(index: number, reason: HandleRetireReason): void {
    const slot = this.#slots[index];
    if (slot === undefined || slot.state === "free") {
      return;
    }

    slot.retiredGeneration = slot.generation;
    slot.retiredReason = reason;
    slot.state = "free";
    slot.type = "";
    slot.value = undefined;
    slot.scope = null;
    slot.rootIndex = -1;
    slot.rootGeneration = -1;
    slot.borrowRefs = 0;

    if (slot.generation === Number.MAX_SAFE_INTEGER) {
      throw new HandleError(
        "HANDLE_STALE",
        "resource slot generation exhausted",
      );
    }
    slot.generation += 1;
    this.#freeIndices.push(index);
  }

  #releaseBorrowRoot(borrowSlot: Slot): void {
    if (borrowSlot.rootIndex < 0) {
      return;
    }
    const source = this.#slots[borrowSlot.rootIndex];
    if (
      source !== undefined &&
      source.generation === borrowSlot.rootGeneration &&
      source.borrowRefs > 0
    ) {
      source.borrowRefs -= 1;
    }
  }

  #resolve(
    handle: ResourceHandle,
    expectedType: ResourceType | undefined,
    expectedKind: HandleUseKind,
  ): { index: number; slot: Slot } {
    const token = readHandle(handle);
    if (token === null || token.owner !== this.#owner) {
      throw new HandleError(
        "HANDLE_INVALID",
        "handle was not issued by this resource table",
        { handle, expectedType },
      );
    }
    const { index, generation } = token;

    const slot = this.#slots[index];
    if (slot === undefined) {
      throw new HandleError("HANDLE_INVALID", "handle index is out of range", {
        handle,
        expectedType,
      });
    }

    if (slot.state === "free") {
      if (generation === slot.retiredGeneration) {
        if (slot.retiredReason === "borrow-ended") {
          throw new HandleError(
            "HANDLE_BORROW_EXPIRED",
            "borrow handle is no longer valid after its call returned",
            { handle, expectedType, actualType: undefined },
          );
        }
        if (slot.retiredReason === "moved") {
          throw new HandleError(
            "HANDLE_MOVED",
            "own handle was invalidated when ownership was transferred",
            { handle, expectedType },
          );
        }
        throw new HandleError(
          "HANDLE_DROPPED",
          "handle was already dropped",
          { handle, expectedType },
        );
      }
      if (generation !== slot.generation) {
        throw new HandleError(
          "HANDLE_STALE",
          "handle generation does not match the current slot generation",
          { handle, expectedType },
        );
      }
      throw new HandleError("HANDLE_INVALID", "handle slot is free", {
        handle,
        expectedType,
      });
    }

    if (generation !== slot.generation) {
      throw new HandleError(
        "HANDLE_STALE",
        "handle generation does not match the current slot generation",
        { handle, expectedType },
      );
    }

    if (expectedType !== undefined && slot.type !== expectedType) {
      throw new HandleError(
        "HANDLE_TYPE_MISMATCH",
        `resource handle has type '${slot.type}', expected '${expectedType}'`,
        { handle, expectedType, actualType: slot.type },
      );
    }

    if (slot.state === "borrow" && (slot.scope === null || !slot.scope.active)) {
      throw new HandleError(
        "HANDLE_BORROW_EXPIRED",
        "borrow handle is no longer valid after its call returned",
        { handle, expectedType, actualType: slot.type },
      );
    }

    if (expectedKind !== "any" && slot.state !== expectedKind) {
      throw new HandleError(
        "HANDLE_KIND_MISMATCH",
        `resource handle is ${slot.state}, expected ${expectedKind}`,
        { handle, expectedType, actualType: slot.type },
      );
    }

    return { index, slot };
  }
}
