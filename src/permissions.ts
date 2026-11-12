export type PermissionMode = "read" | "readwrite";
export type PermissionState = "granted" | "denied" | "prompt";
export type PermissionStateHandleKind = "file" | "directory";
export type InitialPermission = PermissionMode | "prompt";

export interface PermissionDescriptor {
  mode?: PermissionMode;
}

export interface EntryOptions {
  create?: boolean;
}

export interface RootDirectoryOptions {
  /** The permission granted by the user's directory picker. Defaults to read. */
  mode?: InitialPermission;
}

export interface PermissionRequest {
  readonly origin: string;
  readonly mode: PermissionMode;
  readonly handleId: string;
  readonly name: string;
  readonly kind: PermissionStateHandleKind;
  readonly afterRestore: boolean;
}

export type PermissionConfirmer = (
  request: PermissionRequest,
) => boolean | Promise<boolean>;

export interface FileSystemAccessHostOptions {
  origin: string;
  confirmPermission?: PermissionConfirmer;
}

export interface PersistedHandle {
  readonly id: string;
  readonly name: string;
  readonly kind: PermissionStateHandleKind;
  readonly parentId: string | null;
  readonly readGranted: boolean;
  readonly readInvalidatedByAncestor: boolean;
  readonly writeInvalidatedByAncestor: boolean;
}

export interface PersistedPermissionState {
  readonly version: 1;
  readonly origin: string;
  readonly handles: readonly PersistedHandle[];
}

export interface RestoreOptions {
  /**
   * If supplied, it must match the origin in the persisted state. A mismatch
   * is treated as a cross-origin clone attempt.
   */
  origin?: string;
  confirmPermission?: PermissionConfirmer;
}

export class InvalidEntryNameError extends TypeError {
  readonly code = "InvalidEntryNameError";

  constructor(name: string, reason: string) {
    super(
      `Invalid File System Access entry name ${JSON.stringify(name)}: ${reason}`,
    );
    this.name = "InvalidEntryNameError";
  }
}

export class CrossOriginHandleError extends Error {
  readonly code = "CrossOriginHandleError";

  constructor(sourceOrigin: string, targetOrigin: string) {
    super(
      `File System Access handles cannot cross origins (${sourceOrigin} -> ${targetOrigin})`,
    );
    this.name = "CrossOriginHandleError";
  }
}

export class HandleOwnershipError extends Error {
  readonly code = "HandleOwnershipError";

  constructor() {
    super("The File System Access handle is not owned by this host");
    this.name = "HandleOwnershipError";
  }
}

type MutableHandleState = {
  id: string;
  name: string;
  kind: PermissionStateHandleKind;
  parentId: string | null;
  read: boolean;
  write: boolean;
  readInvalidatedByAncestor: boolean;
  writeInvalidatedByAncestor: boolean;
};

type EffectivePermission = {
  read: boolean;
  write: boolean;
};

const handleStates = new WeakMap<object, MutableHandleState>();

const NUL = "\u0000";

/**
 * Rejects names that could be interpreted as a path instead of a single child
 * entry. This prevents `../` escapes from a directory handle to its parent.
 */
export function assertValidEntryName(name: string): void {
  if (typeof name !== "string" || name.length === 0) {
    throw new InvalidEntryNameError(String(name), "name must be non-empty");
  }
  if (name.includes("/") || name.includes("\\")) {
    throw new InvalidEntryNameError(name, "path separators are not allowed");
  }
  if (name.includes(NUL)) {
    throw new InvalidEntryNameError(name, "NUL characters are not allowed");
  }
  if (name === "." || name.includes("..")) {
    throw new InvalidEntryNameError(name, "parent traversal is not allowed");
  }
}

function normalizeOrigin(origin: string): string {
  if (typeof origin !== "string" || origin.trim().length === 0) {
    throw new TypeError("A non-empty origin is required");
  }

  const value = origin.trim();
  try {
    const parsed = new URL(value);
    return parsed.origin === "null" ? value : parsed.origin;
  } catch {
    // Opaque origin strings are matched exactly.
    return value;
  }
}

export function assertSameOrigin(source: string, target: string): void {
  const sourceOrigin = normalizeOrigin(source);
  const targetOrigin = normalizeOrigin(target);
  if (sourceOrigin !== targetOrigin) {
    throw new CrossOriginHandleError(sourceOrigin, targetOrigin);
  }
}

function resolveMode(descriptor?: PermissionDescriptor): PermissionMode {
  const mode = descriptor?.mode ?? "read";
  if (mode !== "read" && mode !== "readwrite") {
    throw new TypeError(`Unsupported permission mode: ${String(mode)}`);
  }
  return mode;
}

function resolveInitialPermission(mode: InitialPermission | undefined): {
  read: boolean;
  write: boolean;
} {
  if (mode === undefined || mode === "read") {
    return { read: true, write: false };
  }
  if (mode === "readwrite") {
    return { read: true, write: true };
  }
  if (mode === "prompt") {
    return { read: false, write: false };
  }
  throw new TypeError(`Unsupported initial permission: ${String(mode)}`);
}

abstract class PermissionedHandleBase {
  readonly #host: PermissionedFileSystemHost;
  readonly #state: MutableHandleState;

  protected constructor(
    host: PermissionedFileSystemHost,
    state: MutableHandleState,
  ) {
    if (!host._owns(state)) {
      throw new HandleOwnershipError();
    }
    handleStates.set(this, state);
    this.#host = host;
    this.#state = state;
  }

  get kind(): PermissionStateHandleKind {
    return this.#state.kind;
  }

  get name(): string {
    return this.#state.name;
  }

  get id(): string {
    return this.#state.id;
  }

  get origin(): string {
    return this.#host.origin;
  }

  protected get host(): PermissionedFileSystemHost {
    return this.#host;
  }

  get ownerOrigin(): string {
    return this.origin;
  }

  async queryPermission(
    descriptor?: PermissionDescriptor,
  ): Promise<PermissionState> {
    return this.#host._permissionFor(this, resolveMode(descriptor));
  }

  async requestPermission(
    descriptor?: PermissionDescriptor,
  ): Promise<PermissionState> {
    return this.#host._requestPermissionFor(
      this,
      resolveMode(descriptor),
    );
  }

  isSameEntry(other: PermissionedHandleBase): boolean {
    const otherState = handleStates.get(other);
    return (
      other instanceof PermissionedHandleBase &&
      otherState !== undefined &&
      this.#host._owns(otherState) &&
      this.#state.id === otherState.id
    );
  }

  /**
   * Returns another wrapper for the same-origin handle. It throws before
   * creating anything when the destination origin differs.
   */
  cloneForOrigin(targetOrigin: string): this {
    assertSameOrigin(this.origin, targetOrigin);
    return this.#host._wrap(this.#state) as this;
  }

  cloneForHost(targetHost: PermissionedFileSystemHost): this {
    if (!(targetHost instanceof PermissionedFileSystemHost)) {
      throw new TypeError("cloneForHost expects a FileSystemAccessHost");
    }
    assertSameOrigin(this.origin, targetHost.origin);
    return this.#host._wrap(this.#state) as this;
  }
}

export class PermissionedFileHandle extends PermissionedHandleBase {
  /** @internal */
  constructor(host: PermissionedFileSystemHost, state: MutableHandleState) {
    super(host, state);
  }
}

export class PermissionedDirectoryHandle extends PermissionedHandleBase {
  /** @internal */
  constructor(host: PermissionedFileSystemHost, state: MutableHandleState) {
    super(host, state);
  }

  async getFileHandle(
    name: string,
    _options?: EntryOptions,
  ): Promise<PermissionedFileHandle> {
    assertValidEntryName(name);
    return this.host._getOrCreateChildHandle(
      this,
      name,
      "file",
    ) as PermissionedFileHandle;
  }

  async getDirectoryHandle(
    name: string,
    _options?: EntryOptions,
  ): Promise<PermissionedDirectoryHandle> {
    assertValidEntryName(name);
    return this.host._getOrCreateChildHandle(
      this,
      name,
      "directory",
    ) as PermissionedDirectoryHandle;
  }
}

type HostState = {
  origin: string;
  confirmPermission?: PermissionConfirmer;
  afterRestore: boolean;
};

export class PermissionedFileSystemHost {
  readonly #hostState: HostState;
  readonly #states = new Map<string, MutableHandleState>();
  #nextId = 1;
  #revocationEpoch = 0;

  constructor(
    originOrOptions: string | FileSystemAccessHostOptions,
    options?: Omit<FileSystemAccessHostOptions, "origin">,
  ) {
    const config =
      typeof originOrOptions === "string"
        ? { origin: originOrOptions, ...options }
        : originOrOptions;

    this.#hostState = {
      origin: normalizeOrigin(config.origin),
      confirmPermission: config.confirmPermission,
      afterRestore: false,
    };
  }

  get origin(): string {
    return this.#hostState.origin;
  }

  /**
   * Creates a root directory handle after the browser's directory picker has
   * returned it. This is the only operation that establishes an initial write
   * grant; child handles can only inherit or request a subset.
   */
  createRootDirectory(
    name: string,
    options: RootDirectoryOptions = {},
  ): PermissionedDirectoryHandle {
    assertValidEntryName(name);
    const initial = resolveInitialPermission(options.mode);
    const state: MutableHandleState = {
      id: this.#allocateId(),
      name,
      kind: "directory",
      parentId: null,
      read: initial.read,
      write: initial.write,
      readInvalidatedByAncestor: false,
      writeInvalidatedByAncestor: false,
    };
    this.#states.set(state.id, state);
    return new PermissionedDirectoryHandle(this, state);
  }

  /**
   * Lowers a handle's permissions and invalidates matching capabilities on all
   * handles that were already derived from it. Re-granting the parent does not
   * silently restore those old descendants.
   */
  revokePermission(
    handle: PermissionedHandleBase,
    mode: PermissionMode | PermissionDescriptor = "readwrite",
  ): PermissionState {
    const state = this.#assertOwned(handle);
    const effectiveMode =
      typeof mode === "string" ? resolveMode({ mode }) : resolveMode(mode);
    this.#revocationEpoch += 1;

    if (effectiveMode === "read") {
      state.read = false;
      state.write = false;
    } else {
      state.write = false;
    }

    for (const candidate of this.#states.values()) {
      if (candidate.id === state.id || !this.#isDescendant(candidate, state)) {
        continue;
      }
      if (effectiveMode === "read") {
        candidate.readInvalidatedByAncestor = true;
        candidate.writeInvalidatedByAncestor = true;
      } else {
        candidate.writeInvalidatedByAncestor = true;
      }
    }

    return this.#permissionForState(state, effectiveMode);
  }

  getHandle(id: string): PermissionedHandleBase | undefined {
    const state = this.#states.get(id);
    return state === undefined ? undefined : this._wrap(state);
  }

  getRootHandles(): readonly PermissionedHandleBase[] {
    return [...this.#states.values()]
      .filter((state) => state.parentId === null)
      .map((state) => this._wrap(state));
  }

  listHandles(): readonly PermissionedHandleBase[] {
    return [...this.#states.values()].map((state) => this._wrap(state));
  }

  /**
   * Creates a JSON-safe snapshot. Write grants are deliberately not persisted;
   * after restore every write request starts at `prompt`.
   */
  persist(): PersistedPermissionState {
    const handles = [...this.#states.values()].map((state) => ({
      id: state.id,
      name: state.name,
      kind: state.kind,
      parentId: state.parentId,
      readGranted: state.read,
      readInvalidatedByAncestor: state.readInvalidatedByAncestor,
      writeInvalidatedByAncestor: state.writeInvalidatedByAncestor,
    }));

    return {
      version: 1,
      origin: this.origin,
      handles,
    };
  }

  persistPermissions(): PersistedPermissionState {
    return this.persist();
  }

  static restore(
    snapshot: PersistedPermissionState,
    options: RestoreOptions = {},
  ): PermissionedFileSystemHost {
    if (snapshot === null || typeof snapshot !== "object") {
      throw new TypeError("Invalid persisted permission state");
    }
    if (snapshot.version !== 1) {
      throw new TypeError(
        `Unsupported persisted permission version: ${String(snapshot.version)}`,
      );
    }

    const snapshotOrigin = normalizeOrigin(snapshot.origin);
    if (options.origin !== undefined) {
      assertSameOrigin(snapshotOrigin, options.origin);
    }
    if (!Array.isArray(snapshot.handles)) {
      throw new TypeError("Persisted handles must be an array");
    }

    const host = new PermissionedFileSystemHost({
      origin: snapshotOrigin,
      confirmPermission: options.confirmPermission,
    });
    host.#hostState.afterRestore = true;

    const records = new Map<string, PersistedHandle>();
    for (const record of snapshot.handles) {
      if (record === null || typeof record !== "object") {
        throw new TypeError("Invalid persisted handle");
      }
      if (typeof record.id !== "string" || record.id.length === 0) {
        throw new TypeError("Persisted handle IDs must be non-empty strings");
      }
      if (records.has(record.id)) {
        throw new TypeError(`Duplicate persisted handle ID: ${record.id}`);
      }
      if (record.kind !== "file" && record.kind !== "directory") {
        throw new TypeError(
          `Invalid persisted handle kind: ${String(record.kind)}`,
        );
      }
      assertValidEntryName(record.name);
      records.set(record.id, record);
    }

    for (const record of records.values()) {
      if (record.parentId !== null) {
        const parent = records.get(record.parentId);
        if (parent === undefined) {
          throw new TypeError(`Missing parent handle for ${record.id}`);
        }
        if (parent.kind !== "directory") {
          throw new TypeError(`Parent of ${record.id} is not a directory`);
        }
      }

      const state: MutableHandleState = {
        id: record.id,
        name: record.name,
        kind: record.kind,
        parentId: record.parentId,
        read: record.readGranted === true,
        // Persisted write grants are intentionally discarded.
        write: false,
        readInvalidatedByAncestor: record.readInvalidatedByAncestor === true,
        writeInvalidatedByAncestor:
          record.writeInvalidatedByAncestor === true,
      };
      host.#states.set(record.id, state);
    }

    host.#assertAcyclicAndTopology();
    host.#nextId = host.#findNextAvailableId();
    return host;
  }

  /** @internal */
  _permissionFor(
    handle: PermissionedHandleBase,
    mode: PermissionMode,
  ): PermissionState {
    return this.#permissionForState(this.#assertOwned(handle), mode);
  }

  #permissionForState(
    state: MutableHandleState,
    mode: PermissionMode,
  ): PermissionState {
    const effective = this.#effectivePermission(state);
    const granted = mode === "read" ? effective.read : effective.write;
    return granted ? "granted" : "prompt";
  }

  /** @internal */
  async _requestPermissionFor(
    handle: PermissionedHandleBase,
    mode: PermissionMode,
  ): Promise<PermissionState> {
    const state = this.#assertOwned(handle);
    if (this.#permissionForState(state, mode) === "granted") {
      return "granted";
    }
    if (!this.#ancestorsPermit(state, mode)) {
      return "prompt";
    }

    const confirmPermission = this.#hostState.confirmPermission;
    if (confirmPermission === undefined) {
      return "prompt";
    }

    const epoch = this.#revocationEpoch;
    const approved = await confirmPermission({
      origin: this.origin,
      mode,
      handleId: state.id,
      name: state.name,
      kind: state.kind,
      afterRestore: this.#hostState.afterRestore,
    });

    // A revocation can happen while an asynchronous confirmation is open.
    // The stale approval must not overwrite the newer, narrower state.
    if (
      approved !== true ||
      epoch !== this.#revocationEpoch ||
      !this.#ancestorsPermit(state, mode)
    ) {
      return this.#permissionForState(state, mode);
    }

    if (mode === "read") {
      state.read = true;
      state.readInvalidatedByAncestor = false;
    } else {
      state.read = true;
      state.write = true;
      state.readInvalidatedByAncestor = false;
      state.writeInvalidatedByAncestor = false;
    }
    return this.#permissionForState(state, mode);
  }

  /** @internal */
  _getOrCreateChildHandle(
    parentHandle: PermissionedHandleBase,
    name: string,
    kind: PermissionStateHandleKind,
  ): PermissionedHandleBase {
    const parent = this.#assertOwned(parentHandle);
    if (parent.kind !== "directory") {
      throw new TypeError("Only directory handles can contain entries");
    }

    const existing = [...this.#states.values()].find(
      (candidate) =>
        candidate.parentId === parent.id && candidate.name === name,
    );
    if (existing !== undefined) {
      if (existing.kind !== kind) {
        throw new TypeError(
          `${JSON.stringify(name)} already exists with kind ${existing.kind}`,
        );
      }
      return this._wrap(existing);
    }

    const inherited = this.#effectivePermission(parent);
    const child: MutableHandleState = {
      id: this.#allocateId(),
      name,
      kind,
      parentId: parent.id,
      read: inherited.read,
      write: inherited.write,
      readInvalidatedByAncestor: false,
      writeInvalidatedByAncestor: false,
    };
    this.#states.set(child.id, child);
    return this._wrap(child);
  }

  /** @internal */
  _wrap(state: MutableHandleState): PermissionedHandleBase {
    this.#assertStateOwned(state);
    return state.kind === "directory"
      ? new PermissionedDirectoryHandle(this, state)
      : new PermissionedFileHandle(this, state);
  }

  /** @internal */
  _owns(state: MutableHandleState): boolean {
    return this.#states.get(state.id) === state;
  }

  #assertOwned(handle: PermissionedHandleBase): MutableHandleState {
    if (!(handle instanceof PermissionedHandleBase)) {
      throw new HandleOwnershipError();
    }
    const state = handleStates.get(handle);
    if (state === undefined) {
      throw new HandleOwnershipError();
    }
    this.#assertStateOwned(state);
    return state;
  }

  #assertStateOwned(state: MutableHandleState): void {
    if (!this._owns(state)) {
      throw new HandleOwnershipError();
    }
  }

  #effectivePermission(state: MutableHandleState): EffectivePermission {
    let read = true;
    let write = true;
    let cursor: MutableHandleState | undefined = state;

    while (cursor !== undefined) {
      read = read && cursor.read && !cursor.readInvalidatedByAncestor;
      write = write && cursor.write && !cursor.writeInvalidatedByAncestor;
      cursor =
        cursor.parentId === null
          ? undefined
          : this.#states.get(cursor.parentId);
    }

    if (!read) {
      write = false;
    }
    return { read, write };
  }

  #ancestorsPermit(
    state: MutableHandleState,
    mode: PermissionMode,
  ): boolean {
    let cursor =
      state.parentId === null ? undefined : this.#states.get(state.parentId);

    while (cursor !== undefined) {
      if (mode === "read") {
        if (!cursor.read || cursor.readInvalidatedByAncestor) {
          return false;
        }
      } else if (
        !cursor.read ||
        !cursor.write ||
        cursor.readInvalidatedByAncestor ||
        cursor.writeInvalidatedByAncestor
      ) {
        return false;
      }
      cursor =
        cursor.parentId === null
          ? undefined
          : this.#states.get(cursor.parentId);
    }
    return true;
  }

  #isDescendant(
    candidate: MutableHandleState,
    ancestor: MutableHandleState,
  ): boolean {
    let cursor = candidate.parentId;
    while (cursor !== null) {
      if (cursor === ancestor.id) {
        return true;
      }
      const parent = this.#states.get(cursor);
      cursor = parent?.parentId ?? null;
    }
    return false;
  }

  #allocateId(): string {
    while (this.#states.has(`handle-${this.#nextId}`)) {
      this.#nextId += 1;
    }
    return `handle-${this.#nextId++}`;
  }

  #findNextAvailableId(): number {
    let next = 1;
    while (this.#states.has(`handle-${next}`)) {
      next += 1;
    }
    return next;
  }

  #assertAcyclicAndTopology(): void {
    for (const state of this.#states.values()) {
      const seen = new Set<string>();
      let cursor: MutableHandleState | undefined = state;
      while (cursor !== undefined) {
        if (seen.has(cursor.id)) {
          throw new TypeError("Persisted handle hierarchy contains a cycle");
        }
        seen.add(cursor.id);
        cursor =
          cursor.parentId === null
            ? undefined
            : this.#states.get(cursor.parentId);
      }
    }
  }
}

export { PermissionedFileSystemHost as FileSystemAccessHost };

export function cloneHandle(
  handle: PermissionedHandleBase,
  target: string | PermissionedFileSystemHost,
): PermissionedHandleBase {
  const targetOrigin = typeof target === "string" ? target : target.origin;
  return handle.cloneForOrigin(targetOrigin);
}

export function restorePermissionState(
  snapshot: PersistedPermissionState,
  options: RestoreOptions = {},
): PermissionedFileSystemHost {
  return PermissionedFileSystemHost.restore(snapshot, options);
}
