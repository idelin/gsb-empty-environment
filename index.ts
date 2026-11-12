export type MidiDocumentId = string | number | object;
export type MidiPortId = string | number | object;
export type MidiData = ArrayLike<number>;

export interface MidiByteStreamFilterOptions {
  passRealtimeDuringSysex?: boolean;
}

export class MidiByteStreamFilter {
  #inSysex = false;
  #runningStatus: number | null = null;
  #passRealtimeDuringSysex: boolean;

  constructor(options: MidiByteStreamFilterOptions = {}) {
    this.#passRealtimeDuringSysex =
      options.passRealtimeDuringSysex !== false;
  }

  get inSysEx(): boolean {
    return this.#inSysex;
  }

  get runningStatus(): number | null {
    return this.#runningStatus;
  }

  filter(data: MidiData): number[] {
    const output: number[] = [];

    for (let index = 0; index < data.length; index += 1) {
      const byte = data[index];
      if (!Number.isInteger(byte) || byte < 0 || byte > 0xff) {
        throw new RangeError(`Invalid MIDI byte at index ${index}: ${byte}`);
      }

      if (byte >= 0xf8) {
        if (!this.#inSysex || this.#passRealtimeDuringSysex) {
          output.push(byte);
        }
        continue;
      }

      if (this.#inSysex) {
        if (byte === 0xf7) {
          this.#inSysex = false;
          this.#runningStatus = null;
        }
        continue;
      }

      if (byte === 0xf0) {
        this.#inSysex = true;
        this.#runningStatus = null;
        continue;
      }

      if (byte === 0xf7) {
        this.#runningStatus = null;
        continue;
      }

      if (byte >= 0xf1) {
        this.#runningStatus = null;
        output.push(byte);
        continue;
      }

      if (byte >= 0x80) {
        this.#runningStatus = byte;
      }
      output.push(byte);
    }

    return output;
  }

  push(data: MidiData): number[] {
    return this.filter(data);
  }
}

function copyValidatedMidiBytes(data: MidiData): number[] {
  const output: number[] = [];
  for (let index = 0; index < data.length; index += 1) {
    const byte = data[index];
    if (!Number.isInteger(byte) || byte < 0 || byte > 0xff) {
      throw new RangeError(`Invalid MIDI byte at index ${index}: ${byte}`);
    }
    output.push(byte);
  }
  return output;
}

export function filterMidiBytes(
  data: MidiData,
  filter: MidiByteStreamFilter = new MidiByteStreamFilter(),
): number[] {
  return filter.filter(data);
}

export const filterMidiData = filterMidiBytes;

export interface SysexPortLease {
  readonly portId: MidiPortId;
  readonly documentId: MidiDocumentId;
  readonly active: boolean;
  release(): boolean;
}

interface LeaseHandle {
  readonly lease: SysexPortLease;
  deactivate(): boolean;
}

interface PortOwner {
  readonly documentId: MidiDocumentId;
  readonly leases: Set<LeaseHandle>;
}

function idsEqual(left: unknown, right: unknown): boolean {
  return left === right;
}

function idToString(id: unknown): string {
  if (typeof id === "string" || typeof id === "number") {
    return String(id);
  }
  return "[object document]";
}

function assertId(id: unknown, name: string): asserts id is MidiDocumentId {
  if (
    id === undefined ||
    id === null ||
    (typeof id !== "string" &&
      typeof id !== "number" &&
      typeof id !== "object")
  ) {
    throw new TypeError(`${name} must be a non-null string, number, or object`);
  }
}

export class SysexPortOwnershipError extends Error {
  readonly code = "ERR_SYSEX_PORT_OWNED";
  readonly portId: MidiPortId;
  readonly documentId: MidiDocumentId;
  readonly ownerDocumentId: MidiDocumentId;

  constructor(
    portId: MidiPortId,
    documentId: MidiDocumentId,
    ownerDocumentId: MidiDocumentId,
  ) {
    super(
      `MIDI port ${idToString(portId)} is exclusively owned by another document`,
    );
    this.name = "SysexPortOwnershipError";
    this.portId = portId;
    this.documentId = documentId;
    this.ownerDocumentId = ownerDocumentId;
  }
}

export class SysexPortRegistry {
  #owners = new Map<MidiPortId, PortOwner>();

  claim(portId: MidiPortId, documentId: MidiDocumentId): SysexPortLease {
    assertId(portId, "portId");
    assertId(documentId, "documentId");

    let owner = this.#owners.get(portId);
    if (owner !== undefined && !idsEqual(owner.documentId, documentId)) {
      throw new SysexPortOwnershipError(
        portId,
        documentId,
        owner.documentId,
      );
    }

    if (owner === undefined) {
      owner = { documentId, leases: new Set() };
      this.#owners.set(portId, owner);
    }

    let active = true;
    let handle: LeaseHandle | undefined;

    const lease: SysexPortLease = {
      portId,
      documentId,
      get active() {
        return active;
      },
      release: () => {
        if (!active) {
          return false;
        }
        active = false;
        this.#removeLease(portId, documentId, handle);
        return true;
      },
    };

    handle = {
      lease,
      deactivate: () => {
        if (!active) {
          return false;
        }
        active = false;
        return true;
      },
    };

    owner.leases.add(handle);
    return lease;
  }

  tryClaim(
    portId: MidiPortId,
    documentId: MidiDocumentId,
  ): SysexPortLease | null {
    try {
      return this.claim(portId, documentId);
    } catch (error) {
      if (error instanceof SysexPortOwnershipError) {
        return null;
      }
      throw error;
    }
  }

  ownerOf(portId: MidiPortId): MidiDocumentId | undefined {
    return this.#owners.get(portId)?.documentId;
  }

  isOwnedBy(portId: MidiPortId, documentId: MidiDocumentId): boolean {
    const owner = this.ownerOf(portId);
    return owner !== undefined && idsEqual(owner, documentId);
  }

  hasOwner(portId: MidiPortId): boolean {
    return this.#owners.has(portId);
  }

  releasePort(portId: MidiPortId, documentId: MidiDocumentId): boolean {
    const owner = this.#owners.get(portId);
    if (owner === undefined || !idsEqual(owner.documentId, documentId)) {
      return false;
    }

    for (const handle of owner.leases) {
      handle.deactivate();
    }
    this.#owners.delete(portId);
    return true;
  }

  releaseDocument(documentId: MidiDocumentId): MidiPortId[] {
    const releasedPorts: MidiPortId[] = [];
    for (const [portId, owner] of this.#owners) {
      if (!idsEqual(owner.documentId, documentId)) {
        continue;
      }
      for (const handle of owner.leases) {
        handle.deactivate();
      }
      this.#owners.delete(portId);
      releasedPorts.push(portId);
    }
    return releasedPorts;
  }

  clear(): void {
    for (const owner of this.#owners.values()) {
      for (const handle of owner.leases) {
        handle.deactivate();
      }
    }
    this.#owners.clear();
  }

  #removeLease(
    portId: MidiPortId,
    documentId: MidiDocumentId,
    handle: LeaseHandle | undefined,
  ): void {
    const owner = this.#owners.get(portId);
    if (owner === undefined || !idsEqual(owner.documentId, documentId)) {
      return;
    }

    if (handle !== undefined) {
      owner.leases.delete(handle);
    }
    if (owner.leases.size === 0) {
      this.#owners.delete(portId);
    }
  }
}

export const defaultSysexPortRegistry = new SysexPortRegistry();

export interface MidiLifecycleEventTarget {
  addEventListener(type: string, listener: (event: unknown) => void): void;
  removeEventListener(type: string, listener: (event: unknown) => void): void;
}

function defaultLifecycleEventTarget(): MidiLifecycleEventTarget | undefined {
  const candidate = globalThis as unknown as {
    addEventListener?: unknown;
    removeEventListener?: unknown;
  };
  if (
    typeof candidate.addEventListener === "function" &&
    typeof candidate.removeEventListener === "function"
  ) {
    return candidate as unknown as MidiLifecycleEventTarget;
  }
  return undefined;
}

export function bindPageHide(
  callback: () => void,
  target: MidiLifecycleEventTarget | undefined = defaultLifecycleEventTarget(),
): () => void {
  if (target === undefined) {
    return () => {};
  }

  let active = true;
  const listener = () => {
    if (!active) {
      return;
    }
    active = false;
    target.removeEventListener("pagehide", listener);
    target.removeEventListener("unload", listener);
    callback();
  };

  target.addEventListener("pagehide", listener);
  target.addEventListener("unload", listener);

  return () => {
    if (!active) {
      return;
    }
    active = false;
    target.removeEventListener("pagehide", listener);
    target.removeEventListener("unload", listener);
  };
}

export function releaseOnPageHide(
  registry: SysexPortRegistry,
  documentId: MidiDocumentId,
  target?: MidiLifecycleEventTarget,
): () => void {
  return bindPageHide(() => {
    registry.releaseDocument(documentId);
  }, target);
}

export interface MidiOutputLike {
  readonly id?: string | number;
  readonly name?: string;
  send(data: number[] | Uint8Array, timestamp?: number): void;
}

export interface WebMidiSendFilterOptions {
  sysexEnabled: boolean;
  documentId: MidiDocumentId;
  portId?: MidiPortId;
  registry?: SysexPortRegistry;
  passRealtimeDuringSysex?: boolean;
  releaseOnPageHide?: boolean;
  lifecycleTarget?: MidiLifecycleEventTarget;
}

export type MidiSendRejectionReason =
  | "sysex-port-owned-by-another-document"
  | "document-session-released";

export class MidiSendRejectedError extends Error {
  readonly code = "ERR_MIDI_SEND_REJECTED";
  readonly reason: MidiSendRejectionReason;
  readonly portId: MidiPortId;
  readonly documentId: MidiDocumentId;
  readonly ownerDocumentId?: MidiDocumentId;

  constructor(
    reason: MidiSendRejectionReason,
    portId: MidiPortId,
    documentId: MidiDocumentId,
    ownerDocumentId?: MidiDocumentId,
  ) {
    const message =
      reason === "document-session-released"
        ? `MIDI sends were released for document ${idToString(documentId)}`
        : `MIDI port ${idToString(portId)} is owned by document ${idToString(
            ownerDocumentId,
          )}; send rejected for document ${idToString(documentId)}`;
    super(message);
    this.name = "MidiSendRejectedError";
    this.reason = reason;
    this.portId = portId;
    this.documentId = documentId;
    if (ownerDocumentId !== undefined) {
      this.ownerDocumentId = ownerDocumentId;
    }
  }
}

const objectPortIds = new WeakMap<object, object>();

function resolvePortId(
  output: MidiOutputLike,
  explicitPortId: MidiPortId | undefined,
): MidiPortId {
  if (explicitPortId !== undefined) {
    assertId(explicitPortId, "portId");
    return explicitPortId;
  }

  if (
    typeof output.id === "string" ||
    typeof output.id === "number"
  ) {
    return output.id;
  }

  let fallback = objectPortIds.get(output);
  if (fallback === undefined) {
    fallback = {};
    objectPortIds.set(output, fallback);
  }
  return fallback;
}

export class WebMidiSendFilter {
  readonly output: MidiOutputLike;
  readonly portId: MidiPortId;
  readonly documentId: MidiDocumentId;
  readonly sysexEnabled: boolean;
  readonly registry: SysexPortRegistry;

  #stream: MidiByteStreamFilter;
  #lease: SysexPortLease | null = null;
  #released = false;
  #unbindLifecycle: () => void;

  constructor(output: MidiOutputLike, options: WebMidiSendFilterOptions) {
    if (output === null || typeof output !== "object") {
      throw new TypeError("output must be a MIDIOutput-like object");
    }
    if (typeof output.send !== "function") {
      throw new TypeError("output.send must be a function");
    }
    if (typeof options.sysexEnabled !== "boolean") {
      throw new TypeError("sysexEnabled must be a boolean");
    }
    assertId(options.documentId, "documentId");

    this.output = output;
    this.portId = resolvePortId(output, options.portId);
    this.documentId = options.documentId;
    this.sysexEnabled = options.sysexEnabled;
    this.registry = options.registry ?? defaultSysexPortRegistry;
    this.#stream = new MidiByteStreamFilter({
      passRealtimeDuringSysex: options.passRealtimeDuringSysex,
    });

    if (this.sysexEnabled) {
      this.#lease = this.registry.tryClaim(this.portId, this.documentId);
    }

    if (options.releaseOnPageHide !== false) {
      this.#unbindLifecycle = bindPageHide(
        () => this.#releaseForNavigation(),
        options.lifecycleTarget,
      );
    } else {
      this.#unbindLifecycle = () => {};
    }
  }

  get inSysEx(): boolean {
    return this.#stream.inSysEx;
  }

  get runningStatus(): number | null {
    return this.#stream.runningStatus;
  }

  get released(): boolean {
    return this.#released;
  }

  get ownsSysexPort(): boolean {
    return (
      this.#lease?.active === true &&
      this.registry.isOwnedBy(this.portId, this.documentId)
    );
  }

  filter(data: MidiData): number[] {
    return this.#filterData(data);
  }

  filterBytes(data: MidiData): number[] {
    return this.#filterData(data);
  }

  send(data: MidiData, timestamp?: number): number[] {
    this.#assertSendAllowed();
    const filtered = this.#filterData(data);
    if (filtered.length === 0) {
      return filtered;
    }

    if (timestamp === undefined) {
      this.output.send(filtered);
    } else {
      this.output.send(filtered, timestamp);
    }
    return filtered;
  }

  release(): boolean {
    if (this.#released) {
      return false;
    }
    this.#released = true;
    this.#unbindLifecycle();
    const lease = this.#lease;
    this.#lease = null;
    lease?.release();
    return true;
  }

  dispose(): boolean {
    return this.release();
  }

  #filterData(data: MidiData): number[] {
    if (this.sysexEnabled) {
      return copyValidatedMidiBytes(data);
    }
    return this.#stream.filter(data);
  }

  #releaseForNavigation(): void {
    if (this.#released) {
      return;
    }
    this.#released = true;
    this.#unbindLifecycle();
    this.#lease?.release();
    this.#lease = null;
    this.registry.releaseDocument(this.documentId);
  }

  #assertSendAllowed(): void {
    if (this.#released) {
      throw new MidiSendRejectedError(
        "document-session-released",
        this.portId,
        this.documentId,
      );
    }

    if (this.#lease !== null && !this.#lease.active) {
      this.#released = true;
      this.#unbindLifecycle();
      throw new MidiSendRejectedError(
        "document-session-released",
        this.portId,
        this.documentId,
      );
    }

    const owner = this.registry.ownerOf(this.portId);
    if (owner !== undefined) {
      if (!idsEqual(owner, this.documentId)) {
        throw new MidiSendRejectedError(
          "sysex-port-owned-by-another-document",
          this.portId,
          this.documentId,
          owner,
        );
      }
      return;
    }

    if (this.sysexEnabled) {
      const lease = this.registry.tryClaim(this.portId, this.documentId);
      if (lease === null) {
        const currentOwner = this.registry.ownerOf(this.portId);
        throw new MidiSendRejectedError(
          "sysex-port-owned-by-another-document",
          this.portId,
          this.documentId,
          currentOwner,
        );
      }
      this.#lease = lease;
    }
  }
}

export function createWebMidiSendFilter(
  output: MidiOutputLike,
  options: WebMidiSendFilterOptions,
): WebMidiSendFilter {
  return new WebMidiSendFilter(output, options);
}

export const wrapWebMidiOutput = createWebMidiSendFilter;

export interface MidiDocumentSessionOptions {
  documentId: MidiDocumentId;
  registry?: SysexPortRegistry;
  releaseOnPageHide?: boolean;
  lifecycleTarget?: MidiLifecycleEventTarget;
}

export type MidiDocumentFilterOptions = Omit<
  WebMidiSendFilterOptions,
  "documentId" | "registry" | "releaseOnPageHide" | "lifecycleTarget"
>;

export class MidiDocumentSession {
  readonly documentId: MidiDocumentId;
  readonly registry: SysexPortRegistry;

  #filters = new Set<WebMidiSendFilter>();
  #closed = false;
  #unbindLifecycle: () => void;

  constructor(options: MidiDocumentSessionOptions) {
    assertId(options.documentId, "documentId");
    this.documentId = options.documentId;
    this.registry = options.registry ?? defaultSysexPortRegistry;

    if (options.releaseOnPageHide !== false) {
      this.#unbindLifecycle = bindPageHide(
        () => this.close(),
        options.lifecycleTarget,
      );
    } else {
      this.#unbindLifecycle = () => {};
    }
  }

  get closed(): boolean {
    return this.#closed;
  }

  createFilter(
    output: MidiOutputLike,
    options: MidiDocumentFilterOptions,
  ): WebMidiSendFilter {
    if (this.#closed) {
      throw new MidiSendRejectedError(
        "document-session-released",
        resolvePortId(output, options.portId),
        this.documentId,
      );
    }

    const filter = new WebMidiSendFilter(output, {
      ...options,
      documentId: this.documentId,
      registry: this.registry,
      releaseOnPageHide: false,
    });
    this.#filters.add(filter);
    return filter;
  }

  createSendFilter(
    output: MidiOutputLike,
    options: MidiDocumentFilterOptions,
  ): WebMidiSendFilter {
    return this.createFilter(output, options);
  }

  close(): boolean {
    if (this.#closed) {
      return false;
    }
    this.#closed = true;
    this.#unbindLifecycle();
    for (const filter of this.#filters) {
      filter.release();
    }
    this.#filters.clear();
    this.registry.releaseDocument(this.documentId);
    return true;
  }

  dispose(): boolean {
    return this.close();
  }
}

export function createMidiDocumentSession(
  options: MidiDocumentSessionOptions,
): MidiDocumentSession {
  return new MidiDocumentSession(options);
}
