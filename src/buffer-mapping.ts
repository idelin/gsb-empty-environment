export const GPUBufferUsage = Object.freeze({
  MAP_READ: 0x0001,
  MAP_WRITE: 0x0002,
  COPY_SRC: 0x0004,
  COPY_DST: 0x0008,
  INDEX: 0x0010,
  VERTEX: 0x0020,
  UNIFORM: 0x0040,
  STORAGE: 0x0080,
  INDIRECT: 0x0100,
  QUERY_RESOLVE: 0x0200,
});

export const GPUMapMode = Object.freeze({
  READ: 0x0001,
  WRITE: 0x0002,
});

export const MAP_OFFSET_ALIGNMENT = 8;
export const MAP_SIZE_ALIGNMENT = 4;
export const MAPPED_AT_CREATION_ALIGNMENT = 4;

let nextPartitionSerial = 1;

const KNOWN_USAGE =
  GPUBufferUsage.MAP_READ |
  GPUBufferUsage.MAP_WRITE |
  GPUBufferUsage.COPY_SRC |
  GPUBufferUsage.COPY_DST |
  GPUBufferUsage.INDEX |
  GPUBufferUsage.VERTEX |
  GPUBufferUsage.UNIFORM |
  GPUBufferUsage.STORAGE |
  GPUBufferUsage.INDIRECT |
  GPUBufferUsage.QUERY_RESOLVE;

export type BufferMapState = "unmapped" | "pending" | "mapped";
export type GpuMapModeValue = 1 | 2;
export type GpuMapMode = GpuMapModeValue | "read" | "write";

export type BufferMappingErrorCode =
  | "ValidationError"
  | "InvalidStateError"
  | "OperationError"
  | "DeviceLostError"
  | "AbortError"
  | "PolicyRevokedError";

export class BufferMappingError extends Error {
  #code: BufferMappingErrorCode;

  constructor(code: BufferMappingErrorCode, message: string) {
    super(message);
    this.name = "BufferMappingError";
    this.#code = code;
  }

  get code(): BufferMappingErrorCode {
    return this.#code;
  }
}

export interface BufferDescriptor {
  size: number;
  usage: number;
  mappedAtCreation?: boolean;
  label?: string;
}

export interface BufferIdentity {
  readonly origin: string;
  readonly partitionId: string;
  readonly deviceId: string;
  readonly deviceEpoch: number;
  readonly slot: number;
  readonly generation: number;
  readonly allocationId: string;
  readonly createdAtTimeline: number;
}

export interface DeviceIdentity {
  readonly origin: string;
  readonly partitionId: string;
  readonly deviceId: string;
  readonly epoch: number;
  readonly timeline: number;
  readonly lost: boolean;
  readonly retired: boolean;
}

export interface MapRequestSnapshot {
  readonly requestId: number;
  readonly promiseGeneration: number;
  readonly origin: string;
  readonly partitionId: string;
  readonly deviceId: string;
  readonly allocationId: string;
  readonly slot: number;
  readonly generation: number;
  readonly mode: GpuMapModeValue;
  readonly offset: number;
  readonly size: number;
  readonly submittedAtTimeline: number;
  readonly submittedDeviceEpoch: number;
  readonly submittedNavigationEpoch: number;
  readonly submittedPolicyEpoch: number;
  readonly state: RequestState;
}

export type RequestState =
  | "pending"
  | "fulfilled"
  | "failed"
  | "cancelled"
  | "stale";

export interface AllocationAuditEntry {
  readonly origin: string;
  readonly partitionId: string;
  readonly deviceId: string;
  readonly deviceEpoch: number;
  readonly allocationId: string;
  readonly slot: number;
  readonly generation: number;
  readonly size: number;
  readonly usage: number;
  readonly status: "live" | "retired" | "released";
  readonly outstandingCallbacks: number;
  readonly createdAtTimeline: number;
  readonly retiredAtTimeline: number | null;
  readonly releasedAtTimeline: number | null;
}

export interface GpuMapCallbackOutcome {
  readonly ok: boolean;
  readonly message?: string;
}

export type GpuMapCallback = (outcome: GpuMapCallbackOutcome) => void;

export interface GpuMapCallbackScheduler {
  schedule(callback: GpuMapCallback): number;
}

export class PausableGpuCallbacks implements GpuMapCallbackScheduler {
  #nextId = 1;
  #entries = new Map<number, GpuMapCallback>();

  schedule(callback: GpuMapCallback): number {
    const id = this.#nextId++;
    this.#entries.set(id, callback);
    return id;
  }

  get pendingCount(): number {
    return this.#entries.size;
  }

  get pendingIds(): number[] {
    return [...this.#entries.keys()];
  }

  complete(
    callbackId: number,
    outcome: boolean | GpuMapCallbackOutcome = true,
  ): boolean {
    const callback = this.#entries.get(callbackId);
    if (!callback) {
      return false;
    }
    this.#entries.delete(callbackId);
    callback(normalizeOutcome(outcome));
    return true;
  }

  completeNext(
    outcome: boolean | GpuMapCallbackOutcome = true,
  ): boolean {
    const first = this.#entries.entries().next();
    if (first.done) {
      return false;
    }
    return this.complete(first.value[0], outcome);
  }

  failNext(message = "GPU mapping failed"): boolean {
    return this.completeNext({ ok: false, message });
  }

  completeAll(
    outcome: boolean | GpuMapCallbackOutcome = true,
  ): number {
    const ids = this.pendingIds;
    for (const id of ids) {
      this.complete(id, outcome);
    }
    return ids.length;
  }

  clear(): number {
    const count = this.#entries.size;
    this.#entries.clear();
    return count;
  }
}

function normalizeOutcome(
  outcome: boolean | GpuMapCallbackOutcome,
): GpuMapCallbackOutcome {
  if (typeof outcome === "boolean") {
    return outcome ? { ok: true } : { ok: false };
  }
  return outcome;
}

function normalizeMode(mode: GpuMapMode): GpuMapModeValue {
  if (mode === "read" || mode === GPUMapMode.READ) {
    return GPUMapMode.READ;
  }
  if (mode === "write" || mode === GPUMapMode.WRITE) {
    return GPUMapMode.WRITE;
  }
  throw failure("ValidationError", "mode must be GPUMapMode.READ or GPUMapMode.WRITE");
}

function failure(
  code: BufferMappingErrorCode,
  message: string,
): BufferMappingError {
  return new BufferMappingError(code, message);
}

function canonicalOrigin(origin: string): string {
  if (typeof origin !== "string") {
    throw failure("ValidationError", "origin must be a string");
  }
  const value = origin.trim();
  if (value.length === 0) {
    throw failure("ValidationError", "origin must not be empty");
  }
  try {
    const parsed = new URL(value);
    if (parsed.origin !== "null") {
      return parsed.origin;
    }
  } catch {
    return value;
  }
  return value;
}

function validateRange(
  limit: number,
  offset: number,
  requestedSize: number | undefined,
): { offset: number; size: number } {
  if (!Number.isSafeInteger(offset) || offset < 0) {
    throw failure("ValidationError", "offset must be a non-negative safe integer");
  }
  if (offset > limit || offset % MAP_OFFSET_ALIGNMENT !== 0) {
    throw failure(
      "ValidationError",
      `offset must be aligned to ${MAP_OFFSET_ALIGNMENT} bytes and inside the buffer`,
    );
  }
  const remaining = limit - offset;
  const size = requestedSize === undefined ? remaining : requestedSize;
  if (!Number.isSafeInteger(size) || size < 0) {
    throw failure("ValidationError", "size must be a non-negative safe integer");
  }
  if (size % MAP_SIZE_ALIGNMENT !== 0) {
    throw failure(
      "ValidationError",
      `size must be aligned to ${MAP_SIZE_ALIGNMENT} bytes`,
    );
  }
  if (size > remaining) {
    throw failure("ValidationError", "mapped range exceeds the buffer bounds");
  }
  return { offset, size };
}

function validateDescriptor(descriptor: BufferDescriptor): {
  size: number;
  usage: number;
  mappedAtCreation: boolean;
  label: string;
} {
  if (descriptor === null || typeof descriptor !== "object") {
    throw failure("ValidationError", "buffer descriptor is required");
  }
  const size = descriptor.size;
  const usage = descriptor.usage;
  if (!Number.isSafeInteger(size) || size <= 0) {
    throw failure("ValidationError", "buffer size must be a positive safe integer");
  }
  if (!Number.isSafeInteger(usage) || usage <= 0) {
    throw failure("ValidationError", "buffer usage must be a non-zero integer bitmask");
  }
  if (usage > 0xffffffff) {
    throw failure("ValidationError", "buffer usage must fit in an unsigned 32-bit mask");
  }
  if ((usage & ~KNOWN_USAGE) !== 0) {
    throw failure("ValidationError", "buffer usage contains unknown flags");
  }
  const hasRead = (usage & GPUBufferUsage.MAP_READ) !== 0;
  const hasWrite = (usage & GPUBufferUsage.MAP_WRITE) !== 0;
  if (hasRead && hasWrite) {
    throw failure(
      "ValidationError",
      "MAP_READ and MAP_WRITE cannot be combined on one buffer",
    );
  }
  if (hasRead && (usage & ~(GPUBufferUsage.MAP_READ | GPUBufferUsage.COPY_DST)) !== 0) {
    throw failure(
      "ValidationError",
      "a MAP_READ buffer may only additionally use COPY_DST",
    );
  }
  if (hasWrite && (usage & ~(GPUBufferUsage.MAP_WRITE | GPUBufferUsage.COPY_SRC)) !== 0) {
    throw failure(
      "ValidationError",
      "a MAP_WRITE buffer may only additionally use COPY_SRC",
    );
  }
  const mappedAtCreation = descriptor.mappedAtCreation === true;
  if (mappedAtCreation && size % MAPPED_AT_CREATION_ALIGNMENT !== 0) {
    throw failure(
      "ValidationError",
      `mappedAtCreation buffers require a size aligned to ${MAPPED_AT_CREATION_ALIGNMENT} bytes`,
    );
  }
  return {
    size,
    usage,
    mappedAtCreation,
    label: typeof descriptor.label === "string" ? descriptor.label : "",
  };
}

function detachArrayBuffer(buffer: ArrayBuffer): void {
  if (buffer.byteLength === 0) {
    return;
  }
  const transfer = (buffer as ArrayBuffer & {
    transfer?: (length: number) => ArrayBuffer;
  }).transfer;
  if (typeof transfer === "function") {
    try {
      transfer.call(buffer, 0);
      return;
    } catch {
      void 0;
    }
  }
  structuredClone(buffer, { transfer: [buffer] });
}

interface AllocationRecord {
  origin: string;
  partitionId: string;
  deviceId: string;
  deviceEpoch: number;
  allocationId: string;
  slot: number;
  generation: number;
  size: number;
  usage: number;
  status: "live" | "retired" | "released";
  outstandingCallbacks: number;
  createdAtTimeline: number;
  retiredAtTimeline: number | null;
  releasedAtTimeline: number | null;
  backing: Uint8Array | null;
}

interface MapRequestInternal {
  requestId: number;
  promiseGeneration: number;
  origin: string;
  partitionId: string;
  deviceId: string;
  allocationId: string;
  slot: number;
  generation: number;
  mode: GpuMapModeValue;
  offset: number;
  size: number;
  submittedAtTimeline: number;
  submittedDeviceEpoch: number;
  submittedNavigationEpoch: number;
  submittedPolicyEpoch: number;
  state: RequestState;
  buffer: GpuBuffer;
  allocation: AllocationRecord;
  callbackOutstanding: boolean;
  resolve: () => void;
  reject: (error: BufferMappingError) => void;
}

interface ActiveMapping {
  mode: GpuMapModeValue;
  offset: number;
  size: number;
  bytes: Uint8Array;
}

interface MappedRangeRecord {
  start: number;
  end: number;
  arrayBuffer: ArrayBuffer;
}

export class GpuDevice {
  readonly host: WebGpuBufferMappingHost;
  readonly deviceId: string;
  label = "";
  #epoch = 0;
  #timeline = 0;
  #lost = false;
  #retired = false;
  #buffers = new Set<GpuBuffer>();

  constructor(host: WebGpuBufferMappingHost, deviceId: string) {
    this.host = host;
    this.deviceId = deviceId;
  }

  get origin(): string {
    return this.host.origin;
  }

  get epoch(): number {
    return this.#epoch;
  }

  get timeline(): number {
    return this.#timeline;
  }

  get lost(): boolean {
    return this.#lost;
  }

  get retired(): boolean {
    return this.#retired;
  }

  get identity(): DeviceIdentity {
    return {
      origin: this.origin,
      partitionId: this.host.partitionId,
      deviceId: this.deviceId,
      epoch: this.#epoch,
      timeline: this.#timeline,
      lost: this.#lost,
      retired: this.#retired,
    };
  }

  createBuffer(descriptor: BufferDescriptor): GpuBuffer {
    return this.host.createBuffer(this, descriptor);
  }

  lose(reason = "device removed"): void {
    this.host.loseDevice(this, reason);
  }

  recover(): void {
    this.host.recoverDevice(this);
  }

  get buffers(): readonly GpuBuffer[] {
    return [...this.#buffers];
  }

  _tick(): number {
    return ++this.#timeline;
  }

  _lose(): void {
    this.#lost = true;
    ++this.#epoch;
    this._tick();
  }

  _recover(): void {
    this.#lost = false;
    ++this.#epoch;
    this._tick();
  }

  _retire(): void {
    if (this.#retired) {
      return;
    }
    this.#retired = true;
    this.#lost = true;
    ++this.#epoch;
    this._tick();
  }

  _track(buffer: GpuBuffer): void {
    this.#buffers.add(buffer);
  }

  _untrack(buffer: GpuBuffer): void {
    this.#buffers.delete(buffer);
  }
}

export interface WebGpuBufferMappingHostOptions {
  origin: string;
  callbacks?: GpuMapCallbackScheduler;
}

export class WebGpuBufferMappingHost {
  readonly origin: string;
  readonly partitionId: string;
  readonly callbacks: GpuMapCallbackScheduler;
  #devices = new Set<GpuDevice>();
  #buffers = new Set<GpuBuffer>();
  #allocations = new Map<string, AllocationRecord>();
  #freeSlots: number[] = [];
  #nextSlot = 0;
  #nextGeneration = 1;
  #nextAllocationId = 1;
  #nextDeviceId = 1;
  #nextPromiseGeneration = 1;
  #nextRequestId = 1;
  #partitionTimeline = 0;
  #navigationEpoch = 0;
  #policyEpoch = 0;
  #policyRevoked = false;

  constructor(
    options: string | WebGpuBufferMappingHostOptions,
    callbacks?: GpuMapCallbackScheduler,
  ) {
    if (typeof options === "string") {
      this.origin = canonicalOrigin(options);
      this.callbacks = callbacks ?? new PausableGpuCallbacks();
    } else {
      this.origin = canonicalOrigin(options.origin);
      this.callbacks = options.callbacks ?? new PausableGpuCallbacks();
    }
    this.partitionId = `${this.origin}#partition-${nextPartitionSerial++}`;
  }

  get navigationEpoch(): number {
    return this.#navigationEpoch;
  }

  get policyEpoch(): number {
    return this.#policyEpoch;
  }

  get policyRevoked(): boolean {
    return this.#policyRevoked;
  }

  get partitionTimeline(): number {
    return this.#partitionTimeline;
  }

  get devices(): readonly GpuDevice[] {
    return [...this.#devices];
  }

  get allocations(): readonly AllocationAuditEntry[] {
    return this.getAllocationAudit();
  }

  createDevice(label = ""): GpuDevice {
    if (this.#policyRevoked) {
      throw failure("PolicyRevokedError", "origin policy is revoked");
    }
    const deviceId = `${this.partitionId}#device-${this.#nextDeviceId++}`;
    const device = new GpuDevice(this, deviceId);
    this.#devices.add(device);
    this.#partitionTimeline++;
    device.label = label;
    return device;
  }

  createBuffer(device: GpuDevice, descriptor: BufferDescriptor): GpuBuffer {
    if (device.host !== this || !this.#devices.has(device)) {
      throw failure(
        "InvalidStateError",
        "device belongs to another origin partition",
      );
    }
    if (this.#policyRevoked) {
      throw failure("PolicyRevokedError", "origin policy is revoked");
    }
    if (device.retired) {
      throw failure("InvalidStateError", "device was retired by navigation or policy");
    }
    if (device.lost) {
      throw failure("DeviceLostError", "device is lost");
    }
    const validated = validateDescriptor(descriptor);
    const slot = this.#freeSlots.pop() ?? this.#nextSlot++;
    const generation = this.#nextGeneration++;
    const allocationId = `${this.partitionId}#alloc-${this.#nextAllocationId++}`;
    const createdAtTimeline = device._tick();
    const allocation: AllocationRecord = {
      origin: this.origin,
      partitionId: this.partitionId,
      deviceId: device.deviceId,
      deviceEpoch: device.epoch,
      allocationId,
      slot,
      generation,
      size: validated.size,
      usage: validated.usage,
      status: "live",
      outstandingCallbacks: 0,
      createdAtTimeline,
      retiredAtTimeline: null,
      releasedAtTimeline: null,
      backing: new Uint8Array(validated.size),
    };
    this.#allocations.set(allocationId, allocation);
    const buffer = new GpuBuffer(
      this,
      device,
      allocation,
      validated.usage,
      validated.label,
    );
    this.#buffers.add(buffer);
    device._track(buffer);
    if (validated.mappedAtCreation) {
      buffer._startMappedAtCreation();
    }
    return buffer;
  }

  mapAsync(
    buffer: GpuBuffer,
    mode: GpuMapMode,
    offset = 0,
    size?: number,
  ): Promise<void> {
    if (buffer.host !== this) {
      return Promise.reject(
        failure("InvalidStateError", "buffer belongs to another origin partition"),
      );
    }
    if (!this.#buffers.has(buffer)) {
      return Promise.reject(failure("InvalidStateError", "buffer is destroyed"));
    }
    if (this.#policyRevoked) {
      return Promise.reject(
        failure("PolicyRevokedError", "origin policy is revoked"),
      );
    }
    if (!buffer.active) {
      return Promise.reject(failure("InvalidStateError", "buffer is destroyed"));
    }
    const device = buffer.device;
    if (device.retired) {
      return Promise.reject(
        failure("InvalidStateError", "device was retired by navigation or policy"),
      );
    }
    if (device.lost) {
      return Promise.reject(failure("DeviceLostError", "device is lost"));
    }
    if (buffer.mapState !== "unmapped") {
      return Promise.reject(
        failure(
          "InvalidStateError",
          `buffer is already ${buffer.mapState === "pending" ? "mapping" : "mapped"}`,
        ),
      );
    }

    let normalizedMode: GpuMapModeValue;
    try {
      normalizedMode = normalizeMode(mode);
    } catch (error) {
      return Promise.reject(error);
    }
    const read = normalizedMode === GPUMapMode.READ;
    if (read && (buffer.usage & GPUBufferUsage.MAP_READ) === 0) {
      return Promise.reject(
        failure("ValidationError", "read mapping requires MAP_READ usage"),
      );
    }
    if (!read && (buffer.usage & GPUBufferUsage.MAP_WRITE) === 0) {
      return Promise.reject(
        failure("ValidationError", "write mapping requires MAP_WRITE usage"),
      );
    }

    let range: { offset: number; size: number };
    try {
      range = validateRange(buffer.size, offset, size);
    } catch (error) {
      return Promise.reject(error);
    }

    return new Promise<void>((resolve, reject) => {
      const allocation = buffer.allocation;
      const request: MapRequestInternal = {
        requestId: this.#nextRequestId++,
        promiseGeneration: this.#nextPromiseGeneration++,
        origin: this.origin,
        partitionId: this.partitionId,
        deviceId: device.deviceId,
        allocationId: allocation.allocationId,
        slot: allocation.slot,
        generation: allocation.generation,
        mode: normalizedMode,
        offset: range.offset,
        size: range.size,
        submittedAtTimeline: device._tick(),
        submittedDeviceEpoch: device.epoch,
        submittedNavigationEpoch: this.#navigationEpoch,
        submittedPolicyEpoch: this.#policyEpoch,
        state: "pending",
        buffer,
        allocation,
        callbackOutstanding: true,
        resolve,
        reject,
      };
      allocation.outstandingCallbacks++;
      buffer._setPending(request);
      try {
        this.callbacks.schedule((outcome) => {
          this.#completeMap(request, outcome);
        });
      } catch (error) {
        if (request.state === "pending") {
          request.state = "failed";
          buffer._clearPending();
          allocation.outstandingCallbacks--;
          request.callbackOutstanding = false;
          reject(
            failure(
              "OperationError",
              error instanceof Error ? error.message : "callback scheduling failed",
            ),
          );
        }
      }
    });
  }

  #completeMap(
    request: MapRequestInternal,
    outcome: GpuMapCallbackOutcome,
  ): void {
    if (!request.callbackOutstanding) {
      return;
    }
    request.callbackOutstanding = false;
    if (request.allocation.outstandingCallbacks > 0) {
      request.allocation.outstandingCallbacks--;
    }

    if (request.state !== "pending") {
      this.#releaseIfRetired(request.allocation);
      return;
    }

    const stale = this.#staleError(request);
    if (stale !== null) {
      request.state = "stale";
      if (request.buffer._isCurrentRequest(request)) {
        request.buffer._clearPending();
      }
      request.reject(stale);
      this.#releaseIfRetired(request.allocation);
      return;
    }

    if (!outcome.ok) {
      request.state = "failed";
      request.buffer._clearPending();
      request.buffer.device._tick();
      request.reject(
        failure(
          "OperationError",
          outcome.message ?? "GPU mapping callback failed",
        ),
      );
      return;
    }

    request.state = "fulfilled";
    try {
      request.buffer._publish(request);
    } catch (error) {
      request.state = "failed";
      request.buffer._clearPending();
      request.reject(
        error instanceof BufferMappingError
          ? error
          : failure("OperationError", "could not publish the mapped range"),
      );
      this.#releaseIfRetired(request.allocation);
      return;
    }
    request.resolve();
  }

  #staleError(request: MapRequestInternal): BufferMappingError | null {
    const buffer = request.buffer;
    const device = buffer.device;
    if (!buffer.active || buffer.allocation.status !== "live") {
      return failure("AbortError", "buffer was destroyed before mapping completed");
    }
    if (
      buffer.identity.generation !== request.generation ||
      buffer.identity.allocationId !== request.allocationId ||
      buffer.identity.origin !== request.origin ||
      buffer.identity.partitionId !== request.partitionId ||
      device.deviceId !== request.deviceId
    ) {
      return failure("AbortError", "buffer generation was replaced");
    }
    if (
      device.lost ||
      device.retired ||
      device.epoch !== request.submittedDeviceEpoch
    ) {
      return failure("DeviceLostError", "device epoch changed before mapping completed");
    }
    if (
      this.#navigationEpoch !== request.submittedNavigationEpoch ||
      buffer.device.retired
    ) {
      return failure("AbortError", "origin partition navigated");
    }
    if (
      this.#policyRevoked ||
      this.#policyEpoch !== request.submittedPolicyEpoch
    ) {
      return failure("PolicyRevokedError", "origin policy was revoked");
    }
    if (!buffer._isCurrentRequest(request) || buffer.mapState !== "pending") {
      return failure("InvalidStateError", "mapping request is no longer pending");
    }
    return null;
  }

  loseDevice(device: GpuDevice, reason = "device removed"): void {
    if (device.host !== this || !this.#devices.has(device)) {
      throw failure("InvalidStateError", "device belongs to another origin partition");
    }
    if (device.retired || device.lost) {
      return;
    }
    device._lose();
    for (const buffer of [...device.buffers]) {
      this._retire(
        buffer,
        failure("DeviceLostError", `device lost: ${reason}`),
      );
    }
    this.#partitionTimeline++;
  }

  recoverDevice(device: GpuDevice): void {
    if (device.host !== this || !this.#devices.has(device)) {
      throw failure("InvalidStateError", "device belongs to another origin partition");
    }
    if (device.retired) {
      throw failure(
        "InvalidStateError",
        "a device retired by navigation or policy cannot be recovered",
      );
    }
    if (!device.lost) {
      return;
    }
    device._recover();
    this.#partitionTimeline++;
  }

  navigate(): void {
    this.#navigationEpoch++;
    this.#partitionTimeline++;
    const error = failure("AbortError", "origin partition navigated");
    this.#cancelAll(error, true);
  }

  revokePolicy(): void {
    if (this.#policyRevoked) {
      return;
    }
    this.#policyRevoked = true;
    this.#policyEpoch++;
    this.#partitionTimeline++;
    const error = failure("PolicyRevokedError", "origin policy was revoked");
    this.#cancelAll(error, true);
  }

  restorePolicy(): void {
    if (!this.#policyRevoked) {
      return;
    }
    this.#policyRevoked = false;
    this.#policyEpoch++;
    this.#partitionTimeline++;
  }

  #cancelAll(error: BufferMappingError, retireDevices: boolean): void {
    for (const buffer of [...this.#buffers]) {
      this._retire(buffer, error);
    }
    if (retireDevices) {
      for (const device of [...this.#devices]) {
        device._retire();
      }
    }
  }

  _releaseSlot(allocation: AllocationRecord): void {
    if (allocation.status === "released") {
      return;
    }
    this.#freeSlots.push(allocation.slot);
  }

  _releaseAllocation(allocation: AllocationRecord): void {
    if (allocation.status === "released") {
      return;
    }
    allocation.status = "released";
    allocation.backing = null;
    allocation.releasedAtTimeline = this.#partitionTimeline++;
  }

  #releaseIfRetired(allocation: AllocationRecord): void {
    if (
      allocation.status === "retired" &&
      allocation.outstandingCallbacks === 0
    ) {
      this._releaseAllocation(allocation);
    }
  }

  _retire(
    buffer: GpuBuffer,
    error = failure("AbortError", "buffer destroyed before mapping completed"),
  ): void {
    const allocation = buffer.allocation;
    if (!buffer.active) {
      return;
    }
    buffer._cancelForLifecycle(error);
    buffer._deactivate();
    if (allocation.status === "live") {
      allocation.status = "retired";
      allocation.retiredAtTimeline = this.#partitionTimeline++;
      allocation.backing = null;
      this._releaseSlot(allocation);
    }
    this.#buffers.delete(buffer);
    buffer.device._untrack(buffer);
    if (allocation.outstandingCallbacks === 0) {
      this._releaseAllocation(allocation);
    }
  }

  getAllocationAudit(): AllocationAuditEntry[] {
    return [...this.#allocations.values()].map((allocation) => ({
      origin: allocation.origin,
      partitionId: allocation.partitionId,
      deviceId: allocation.deviceId,
      deviceEpoch: allocation.deviceEpoch,
      allocationId: allocation.allocationId,
      slot: allocation.slot,
      generation: allocation.generation,
      size: allocation.size,
      usage: allocation.usage,
      status: allocation.status,
      outstandingCallbacks: allocation.outstandingCallbacks,
      createdAtTimeline: allocation.createdAtTimeline,
      retiredAtTimeline: allocation.retiredAtTimeline,
      releasedAtTimeline: allocation.releasedAtTimeline,
    }));
  }
}

export class GpuBuffer {
  readonly host: WebGpuBufferMappingHost;
  readonly device: GpuDevice;
  readonly allocation: AllocationRecord;
  readonly size: number;
  readonly usage: number;
  readonly label: string;
  #mapState: BufferMapState = "unmapped";
  #active = true;
  #currentRequest: MapRequestInternal | null = null;
  #lastRequest: MapRequestInternal | null = null;
  #activeMapping: ActiveMapping | null = null;
  #mappedRanges: MappedRangeRecord[] = [];

  constructor(
    host: WebGpuBufferMappingHost,
    device: GpuDevice,
    allocation: AllocationRecord,
    usage: number,
    label: string,
  ) {
    this.host = host;
    this.device = device;
    this.allocation = allocation;
    this.size = allocation.size;
    this.usage = usage;
    this.label = label;
  }

  get active(): boolean {
    return this.#active;
  }

  _deactivate(): void {
    this.#active = false;
  }

  get mapState(): BufferMapState {
    return this.#mapState;
  }

  get identity(): BufferIdentity {
    return {
      origin: this.allocation.origin,
      partitionId: this.allocation.partitionId,
      deviceId: this.allocation.deviceId,
      deviceEpoch: this.allocation.deviceEpoch,
      slot: this.allocation.slot,
      generation: this.allocation.generation,
      allocationId: this.allocation.allocationId,
      createdAtTimeline: this.allocation.createdAtTimeline,
    };
  }

  get generation(): number {
    return this.allocation.generation;
  }

  get allocationId(): string {
    return this.allocation.allocationId;
  }

  get slot(): number {
    return this.allocation.slot;
  }

  get pendingMapRequest(): MapRequestSnapshot | null {
    return this.#currentRequest
      ? snapshotRequest(this.#currentRequest)
      : null;
  }

  get lastMapRequest(): MapRequestSnapshot | null {
    return this.#lastRequest ? snapshotRequest(this.#lastRequest) : null;
  }

  get mappedRangeSize(): number {
    return this.#activeMapping ? this.#activeMapping.size : 0;
  }

  get mappedRangeOffset(): number {
    return this.#activeMapping ? this.#activeMapping.offset : 0;
  }

  mapAsync(
    mode: GpuMapMode,
    offset = 0,
    size?: number,
  ): Promise<void> {
    return this.host.mapAsync(this, mode, offset, size);
  }

  getMappedRange(offset = 0, size?: number): ArrayBuffer {
    if (!this.#active || this.mapState !== "mapped" || !this.#activeMapping) {
      throw failure("InvalidStateError", "buffer is not mapped");
    }
    if (this.device.lost || this.device.retired) {
      throw failure("DeviceLostError", "device is lost or retired");
    }
    const mapping = this.#activeMapping;
    const range = validateRange(mapping.size, offset, size);
    const start = mapping.offset + range.offset;
    const end = start + range.size;
    for (const existing of this.#mappedRanges) {
      if (start < existing.end && existing.start < end) {
        throw failure(
          "OperationError",
          "getMappedRange ranges must not overlap",
        );
      }
    }
    const arrayBuffer = new ArrayBuffer(range.size);
    if (range.size > 0) {
      new Uint8Array(arrayBuffer).set(
        mapping.bytes.subarray(range.offset, range.offset + range.size),
      );
    }
    this.#mappedRanges.push({ start, end, arrayBuffer });
    return arrayBuffer;
  }

  unmap(): void {
    if (!this.#active) {
      return;
    }
    if (this.mapState === "pending") {
      this._cancelPendingOnly(
        failure("AbortError", "unmap cancelled the pending mapping"),
      );
      this.device._tick();
      return;
    }
    if (this.mapState !== "mapped") {
      return;
    }
    this._commitMapping();
    this._detachMappings();
    this.#mapState = "unmapped";
    this.device._tick();
  }

  destroy(): void {
    if (!this.#active) {
      return;
    }
    this.host._retire(this);
    this.device._tick();
  }

  _setPending(request: MapRequestInternal): void {
    this.#mapState = "pending";
    this.#currentRequest = request;
    this.#lastRequest = request;
  }

  _clearPending(): void {
    this.#currentRequest = null;
    if (this.#mapState === "pending") {
      this.#mapState = "unmapped";
    }
  }

  _isCurrentRequest(request: MapRequestInternal): boolean {
    return this.#currentRequest === request && this.#mapState === "pending";
  }

  _startMappedAtCreation(): void {
    this.#activeMapping = {
      mode: GPUMapMode.WRITE,
      offset: 0,
      size: this.size,
      bytes: new Uint8Array(this.size),
    };
    this.#mapState = "mapped";
  }

  _publish(request: MapRequestInternal): void {
    const source = request.allocation.backing;
    if (!source) {
      throw failure("InvalidStateError", "allocation backing was retired");
    }
    const bytes = source.slice(request.offset, request.offset + request.size);
    this.#activeMapping = {
      mode: request.mode,
      offset: request.offset,
      size: request.size,
      bytes,
    };
    this.#mapState = "mapped";
    this.#currentRequest = null;
    this.#lastRequest = request;
    this.device._tick();
  }

  _cancelPendingOnly(error: BufferMappingError): void {
    const request = this.#currentRequest;
    if (!request || request.state !== "pending") {
      return;
    }
    request.state = "cancelled";
    this.#currentRequest = null;
    this.#mapState = "unmapped";
    request.reject(error);
  }

  _cancelForLifecycle(error: BufferMappingError): void {
    if (!this.#active) {
      return;
    }
    this._cancelPendingOnly(error);
    if (this.mapState === "mapped") {
      this._detachMappings();
      this.#mapState = "unmapped";
      this.device._tick();
    }
  }

  _commitMapping(): void {
    const mapping = this.#activeMapping;
    if (!mapping) {
      return;
    }
    if (mapping.mode !== GPUMapMode.WRITE) {
      return;
    }
    for (const range of this.#mappedRanges) {
      if (range.arrayBuffer.byteLength === 0) {
        continue;
      }
      const relativeOffset = range.start - mapping.offset;
      const source = new Uint8Array(range.arrayBuffer);
      mapping.bytes.set(source, relativeOffset);
    }
    const destination = this.allocation.backing;
    if (destination) {
      destination.set(mapping.bytes, mapping.offset);
    }
  }

  _detachMappings(): void {
    for (const range of this.#mappedRanges) {
      detachArrayBuffer(range.arrayBuffer);
    }
    this.#mappedRanges = [];
    this.#activeMapping = null;
  }
}

function snapshotRequest(request: MapRequestInternal): MapRequestSnapshot {
  return {
    requestId: request.requestId,
    promiseGeneration: request.promiseGeneration,
    origin: request.origin,
    partitionId: request.partitionId,
    deviceId: request.deviceId,
    allocationId: request.allocationId,
    slot: request.slot,
    generation: request.generation,
    mode: request.mode,
    offset: request.offset,
    size: request.size,
    submittedAtTimeline: request.submittedAtTimeline,
    submittedDeviceEpoch: request.submittedDeviceEpoch,
    submittedNavigationEpoch: request.submittedNavigationEpoch,
    submittedPolicyEpoch: request.submittedPolicyEpoch,
    state: request.state,
  };
}

export { WebGpuBufferMappingHost as GpuBufferMappingHost };
export { WebGpuBufferMappingHost as OriginIsolationPartition };
export { WebGpuBufferMappingHost as WebGpuBufferMappingLifecycle };
export { BufferMappingError as GpuBufferMappingError };
export { detachArrayBuffer as detachBufferView };
export const BufferUsage = GPUBufferUsage;
export const MapMode = GPUMapMode;

export default WebGpuBufferMappingHost;
