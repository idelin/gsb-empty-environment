import test from "node:test";
import assert from "node:assert/strict";
import {
  BufferMappingError,
  GPUBufferUsage,
  GPUMapMode,
  PausableGpuCallbacks,
  WebGpuBufferMappingHost,
} from "../index.ts";

const U = GPUBufferUsage;

function makeHost(origin = "https://a.example") {
  const callbacks = new PausableGpuCallbacks();
  const host = new WebGpuBufferMappingHost({ origin, callbacks });
  return { host, callbacks, device: host.createDevice() };
}

function writeBuffer(device, size = 64, mappedAtCreation = false) {
  return device.createBuffer({
    size,
    usage: U.MAP_WRITE | U.COPY_SRC,
    mappedAtCreation,
  });
}

function readBuffer(device, size = 64) {
  return device.createBuffer({
    size,
    usage: U.MAP_READ | U.COPY_DST,
  });
}

function allocation(host, allocationId) {
  const entry = host.allocations.find(
    (candidate) => candidate.allocationId === allocationId,
  );
  assert.ok(entry);
  return entry;
}

function rejectsWithCode(promise, code) {
  return assert.rejects(
    promise,
    (error) => error instanceof BufferMappingError && error.code === code,
  );
}

test("late success after destroy only releases the old allocation", async () => {
  const { host, callbacks, device } = makeHost();
  const oldBuffer = writeBuffer(device);
  const oldDone = oldBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  const oldRejection = rejectsWithCode(oldDone, "AbortError");
  const oldRequest = oldBuffer.pendingMapRequest;
  assert.ok(oldRequest);
  assert.equal(oldRequest.generation, oldBuffer.generation);
  assert.equal(oldRequest.allocationId, oldBuffer.allocationId);
  assert.equal(oldBuffer.mapState, "pending");

  oldBuffer.destroy();
  await oldRejection;

  const freshBuffer = writeBuffer(device);
  assert.equal(freshBuffer.slot, oldBuffer.slot);
  assert.notEqual(freshBuffer.generation, oldBuffer.generation);
  assert.notEqual(freshBuffer.allocationId, oldBuffer.allocationId);

  const freshDone = freshBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  const freshRequest = freshBuffer.pendingMapRequest;
  assert.ok(freshRequest);
  assert.ok(freshRequest.promiseGeneration > oldRequest.promiseGeneration);
  assert.equal(callbacks.pendingCount, 2);

  assert.ok(callbacks.completeNext());
  assert.equal(allocation(host, oldBuffer.allocationId).status, "released");
  assert.equal(freshBuffer.mapState, "pending");
  assert.equal(allocation(host, freshBuffer.allocationId).status, "live");

  assert.ok(callbacks.completeNext());
  await freshDone;
  assert.equal(freshBuffer.mapState, "mapped");
  const view = new Uint8Array(freshBuffer.getMappedRange());
  view.set([0x44, 0x55, 0x66, 0x77]);
  assert.deepEqual([...view.slice(0, 4)], [0x44, 0x55, 0x66, 0x77]);
  freshBuffer.unmap();
  assert.equal(view.byteLength, 0);
});

test("destroy detaches every mapped view and a rebuilt slot has new bytes", async () => {
  const { callbacks, device } = makeHost();
  const oldBuffer = writeBuffer(device);
  const done = oldBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  callbacks.completeNext();
  await done;

  const first = oldBuffer.getMappedRange(0, 16);
  const second = oldBuffer.getMappedRange(16, 16);
  const firstBytes = new Uint8Array(first);
  const secondBytes = new Uint8Array(second);
  firstBytes.fill(0x11);
  secondBytes.fill(0x22);

  oldBuffer.destroy();
  assert.equal(first.byteLength, 0);
  assert.equal(second.byteLength, 0);
  assert.equal(firstBytes.length, 0);
  assert.equal(secondBytes.length, 0);
  assert.equal(firstBytes[0], undefined);

  const freshBuffer = writeBuffer(device);
  assert.equal(freshBuffer.slot, oldBuffer.slot);
  assert.notEqual(freshBuffer.generation, oldBuffer.generation);
  const freshDone = freshBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  callbacks.completeNext();
  await freshDone;

  const freshBytes = new Uint8Array(freshBuffer.getMappedRange());
  freshBytes.fill(0x99);
  assert.equal(firstBytes.length, 0);
  assert.equal(secondBytes.length, 0);
  assert.equal(freshBytes[0], 0x99);
  freshBuffer.unmap();
  assert.equal(freshBytes.length, 0);
});

test("device loss detaches mappings and stale success cannot revive a slot", async () => {
  const { host, callbacks, device } = makeHost();
  const mappedBuffer = writeBuffer(device);
  const mappedDone = mappedBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  callbacks.completeNext();
  await mappedDone;
  const detached = new Uint8Array(mappedBuffer.getMappedRange());
  detached.fill(0xaa);
  const pendingBuffer = writeBuffer(device);
  const pendingDone = pendingBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  const pendingRejection = rejectsWithCode(pendingDone, "DeviceLostError");
  const oldEpoch = device.epoch;
  const pendingRequest = pendingBuffer.pendingMapRequest;
  assert.ok(pendingRequest);

  device.lose("simulated reset");
  await pendingRejection;
  assert.equal(detached.length, 0);
  assert.ok(device.lost);
  assert.ok(device.epoch > oldEpoch);
  assert.equal(pendingBuffer.mapState, "unmapped");
  assert.equal(mappedBuffer.mapState, "unmapped");
  assert.equal(allocation(host, pendingBuffer.allocationId).status, "retired");

  device.recover();
  assert.equal(device.lost, false);
  assert.ok(device.epoch > oldEpoch);

  const freshBuffer = writeBuffer(device);
  assert.ok(
    freshBuffer.slot === pendingBuffer.slot ||
      freshBuffer.slot === mappedBuffer.slot,
  );
  assert.ok(freshBuffer.generation > mappedBuffer.generation);
  const freshDone = freshBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);

  assert.ok(callbacks.pendingCount >= 1);
  callbacks.completeAll();
  await freshDone;
  assert.equal(freshBuffer.mapState, "mapped");
  assert.equal(detached.length, 0);
  assert.equal(
    allocation(host, pendingBuffer.allocationId).status,
    "released",
  );
  assert.equal(
    allocation(host, mappedBuffer.allocationId).status,
    "released",
  );
  const freshBytes = new Uint8Array(freshBuffer.getMappedRange());
  freshBytes.fill(0x77);
  assert.equal(freshBytes[0], 0x77);
  assert.equal(detached[0], undefined);
});

test("navigation cancels pending maps, detaches views, and isolates rebuilt slots", async () => {
  const { host, callbacks, device } = makeHost();
  const mappedBuffer = writeBuffer(device);
  const mappedDone = mappedBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  callbacks.completeNext();
  await mappedDone;
  const detached = new Uint8Array(mappedBuffer.getMappedRange());
  const pendingBuffer = writeBuffer(device);
  const pendingDone = pendingBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  const pendingRejection = rejectsWithCode(pendingDone, "AbortError");

  host.navigate();
  await pendingRejection;
  assert.equal(detached.length, 0);
  assert.ok(device.retired);
  assert.equal(pendingBuffer.active, false);
  assert.equal(mappedBuffer.active, false);
  assert.throws(
    () => mappedBuffer.getMappedRange(),
    (error) => error instanceof BufferMappingError && error.code === "InvalidStateError",
  );

  const newDevice = host.createDevice();
  assert.notEqual(newDevice.deviceId, device.deviceId);
  const freshBuffer = writeBuffer(newDevice);
  assert.equal(freshBuffer.slot, pendingBuffer.slot);
  assert.ok(freshBuffer.generation > mappedBuffer.generation);
  const freshDone = freshBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  assert.equal(freshBuffer.mapState, "pending");

  callbacks.completeAll();
  await freshDone;
  assert.equal(freshBuffer.mapState, "mapped");
  const freshBytes = new Uint8Array(freshBuffer.getMappedRange());
  freshBytes.fill(0x31);
  assert.equal(detached.length, 0);
  assert.equal(detached[0], undefined);
});

test("policy revocation atomically aborts maps and restore starts a new epoch", async () => {
  const { host, callbacks, device } = makeHost();
  const mappedBuffer = writeBuffer(device);
  const mappedDone = mappedBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  callbacks.completeNext();
  await mappedDone;
  const detached = new Uint8Array(mappedBuffer.getMappedRange());
  const pendingBuffer = writeBuffer(device);
  const pendingDone = pendingBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  const pendingRejection = rejectsWithCode(pendingDone, "PolicyRevokedError");
  const oldPolicyEpoch = host.policyEpoch;

  host.revokePolicy();
  await pendingRejection;
  assert.equal(detached.length, 0);
  assert.ok(host.policyRevoked);
  assert.ok(host.policyEpoch > oldPolicyEpoch);
  assert.throws(
    () => host.createDevice(),
    (error) => error instanceof BufferMappingError && error.code === "PolicyRevokedError",
  );

  const staleCallbacks = callbacks.pendingCount;
  assert.equal(staleCallbacks, 1);
  host.restorePolicy();
  assert.equal(host.policyRevoked, false);

  const newDevice = host.createDevice();
  const freshBuffer = writeBuffer(newDevice);
  const freshDone = freshBuffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  assert.equal(freshBuffer.mapState, "pending");
  callbacks.completeAll();
  await freshDone;
  assert.equal(freshBuffer.mapState, "mapped");
  const freshBytes = new Uint8Array(freshBuffer.getMappedRange());
  freshBytes.fill(0x42);
  assert.equal(freshBytes[0], 0x42);
  assert.equal(detached[0], undefined);
});

test("mappedAtCreation follows mapped state and repeated unmap is idempotent", async () => {
  const { callbacks, device } = makeHost();
  const buffer = writeBuffer(device, 64, true);
  assert.equal(buffer.mapState, "mapped");
  await rejectsWithCode(
    buffer.mapAsync(GPUMapMode.WRITE, 0, 64),
    "InvalidStateError",
  );

  const first = buffer.getMappedRange(0, 16);
  const firstBytes = new Uint8Array(first);
  firstBytes.set([1, 2, 3, 4, 5, 6, 7, 8]);
  assert.throws(
    () => buffer.getMappedRange(8, 16),
    (error) => error instanceof BufferMappingError && error.code === "OperationError",
  );
  assert.equal(new Uint8Array(buffer.getMappedRange(16, 16)).length, 16);

  buffer.unmap();
  buffer.unmap();
  buffer.unmap();
  assert.equal(firstBytes.length, 0);
  assert.equal(buffer.mapState, "unmapped");
  assert.throws(
    () => buffer.getMappedRange(),
    (error) => error instanceof BufferMappingError && error.code === "InvalidStateError",
  );

  const done = buffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  callbacks.completeNext();
  await done;
  const preserved = new Uint8Array(buffer.getMappedRange(0, 16));
  assert.deepEqual([...preserved], [1, 2, 3, 4, 5, 6, 7, 8, 0, 0, 0, 0, 0, 0, 0, 0]);
  buffer.unmap();
  assert.equal(preserved.length, 0);
});

test("range validation, boundaries, overlap, and overflow are deterministic", async () => {
  const { callbacks, device } = makeHost();
  const buffer = writeBuffer(device, 64);

  await rejectsWithCode(
    buffer.mapAsync(GPUMapMode.WRITE, 4, 8),
    "ValidationError",
  );
  await rejectsWithCode(
    buffer.mapAsync(GPUMapMode.WRITE, 0, 6),
    "ValidationError",
  );
  await rejectsWithCode(
    buffer.mapAsync(GPUMapMode.WRITE, 56, 16),
    "ValidationError",
  );
  await rejectsWithCode(
    buffer.mapAsync(GPUMapMode.WRITE, 64, 8),
    "ValidationError",
  );
  await rejectsWithCode(
    buffer.mapAsync(GPUMapMode.WRITE, 0, Number.MAX_SAFE_INTEGER),
    "ValidationError",
  );
  await rejectsWithCode(
    buffer.mapAsync(GPUMapMode.WRITE, -8, 8),
    "ValidationError",
  );
  assert.equal(buffer.mapState, "unmapped");
  assert.equal(callbacks.pendingCount, 0);

  const legalDone = buffer.mapAsync(GPUMapMode.WRITE, 8, 56);
  assert.equal(buffer.mapState, "pending");
  callbacks.completeNext();
  await legalDone;
  assert.equal(buffer.mappedRangeOffset, 8);
  assert.equal(buffer.mappedRangeSize, 56);

  const first = buffer.getMappedRange(0, 16);
  const third = buffer.getMappedRange(16, 16);
  const tail = buffer.getMappedRange(48, 8);
  assert.equal(first.byteLength, 16);
  assert.equal(third.byteLength, 16);
  assert.equal(tail.byteLength, 8);
  assert.throws(
    () => buffer.getMappedRange(8, 16),
    (error) => error instanceof BufferMappingError && error.code === "OperationError",
  );
  assert.throws(
    () => buffer.getMappedRange(56, 8),
    (error) => error instanceof BufferMappingError && error.code === "ValidationError",
  );
  assert.throws(
    () => buffer.getMappedRange(4, 8),
    (error) => error instanceof BufferMappingError && error.code === "ValidationError",
  );
  assert.throws(
    () => buffer.getMappedRange(0, 6),
    (error) => error instanceof BufferMappingError && error.code === "ValidationError",
  );
  buffer.unmap();
  assert.equal(first.byteLength, 0);
  assert.equal(third.byteLength, 0);
  assert.equal(tail.byteLength, 0);

  const tailDone = buffer.mapAsync(GPUMapMode.WRITE, 32, 32);
  callbacks.completeNext();
  await tailDone;
  assert.equal(buffer.getMappedRange(0, 32).byteLength, 32);
  buffer.unmap();

  const emptyDone = buffer.mapAsync(GPUMapMode.WRITE, 64, 0);
  callbacks.completeNext();
  await emptyDone;
  assert.equal(buffer.mapState, "mapped");
  assert.equal(buffer.getMappedRange().byteLength, 0);
  buffer.unmap();

  const wholeDone = buffer.mapAsync(GPUMapMode.WRITE);
  callbacks.completeNext();
  await wholeDone;
  assert.equal(buffer.mappedRangeOffset, 0);
  assert.equal(buffer.mappedRangeSize, 64);
  buffer.unmap();
});

test("overlapping mapAsync requests are rejected without corrupting state", async () => {
  const { callbacks, device } = makeHost();
  const buffer = writeBuffer(device, 64);
  const first = buffer.mapAsync(GPUMapMode.WRITE, 0, 32);
  assert.equal(buffer.mapState, "pending");

  await rejectsWithCode(
    buffer.mapAsync(GPUMapMode.WRITE, 16, 16),
    "InvalidStateError",
  );
  assert.equal(buffer.mapState, "pending");
  assert.equal(callbacks.pendingCount, 1);

  callbacks.completeNext();
  await first;
  assert.equal(buffer.mapState, "mapped");
  await rejectsWithCode(
    buffer.mapAsync(GPUMapMode.WRITE, 0, 16),
    "InvalidStateError",
  );
  assert.equal(buffer.mapState, "mapped");
  assert.equal(buffer.getMappedRange(0, 16).byteLength, 16);
  buffer.unmap();
  assert.equal(buffer.mapState, "unmapped");
});

test("read and write mappings enforce usage, preserve direction, and recover from callback failure", async () => {
  const { callbacks, device } = makeHost();
  const readable = readBuffer(device);
  const writable = writeBuffer(device);
  const gpuOnly = device.createBuffer({
    size: 32,
    usage: U.STORAGE | U.COPY_SRC | U.COPY_DST,
  });

  await rejectsWithCode(
    readable.mapAsync(GPUMapMode.WRITE, 0, 32),
    "ValidationError",
  );
  await rejectsWithCode(
    writable.mapAsync(GPUMapMode.READ, 0, 32),
    "ValidationError",
  );
  await rejectsWithCode(
    gpuOnly.mapAsync(GPUMapMode.READ, 0, 32),
    "ValidationError",
  );
  assert.throws(
    () =>
      device.createBuffer({
        size: 32,
        usage: U.MAP_READ | U.MAP_WRITE,
      }),
    (error) => error instanceof BufferMappingError && error.code === "ValidationError",
  );
  assert.throws(
    () =>
      device.createBuffer({
        size: 30,
        usage: U.MAP_WRITE | U.COPY_SRC,
        mappedAtCreation: true,
      }),
    (error) => error instanceof BufferMappingError && error.code === "ValidationError",
  );

  const readDone = readable.mapAsync(GPUMapMode.READ, 0, 32);
  callbacks.completeNext();
  await readDone;
  const readView = new Uint8Array(readable.getMappedRange());
  readView.fill(0xaa);
  readable.unmap();
  assert.equal(readView.length, 0);

  const readAgain = readable.mapAsync(GPUMapMode.READ, 0, 32);
  callbacks.completeNext();
  await readAgain;
  assert.deepEqual(
    [...new Uint8Array(readable.getMappedRange())],
    new Array(32).fill(0),
  );
  readable.unmap();

  const writeDone = writable.mapAsync(GPUMapMode.WRITE, 0, 32);
  callbacks.completeNext();
  await writeDone;
  new Uint8Array(writable.getMappedRange()).fill(0x5a);
  writable.unmap();

  const writeAgain = writable.mapAsync(GPUMapMode.WRITE, 0, 32);
  callbacks.completeNext();
  await writeAgain;
  assert.deepEqual(
    [...new Uint8Array(writable.getMappedRange())],
    new Array(32).fill(0x5a),
  );
  writable.unmap();

  const failed = writable.mapAsync(GPUMapMode.WRITE, 0, 32);
  callbacks.failNext("synthetic GPU fault");
  await rejectsWithCode(failed, "OperationError");
  assert.equal(writable.mapState, "unmapped");
  const afterFailure = writable.mapAsync(GPUMapMode.WRITE, 0, 32);
  callbacks.completeNext();
  await afterFailure;
  assert.equal(writable.mapState, "mapped");
  writable.unmap();
});

test("a pending unmap rejects late work without retiring a live allocation", async () => {
  const { host, callbacks, device } = makeHost();
  const buffer = writeBuffer(device);
  const first = buffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  const firstRejection = rejectsWithCode(first, "AbortError");
  const firstRequest = buffer.pendingMapRequest;
  assert.ok(firstRequest);
  buffer.unmap();
  await firstRejection;
  assert.equal(buffer.mapState, "unmapped");
  assert.equal(allocation(host, buffer.allocationId).status, "live");
  assert.equal(callbacks.pendingCount, 1);

  const second = buffer.mapAsync(GPUMapMode.WRITE, 0, 64);
  const secondRequest = buffer.pendingMapRequest;
  assert.ok(secondRequest);
  assert.ok(secondRequest.promiseGeneration > firstRequest.promiseGeneration);
  callbacks.completeNext();
  assert.equal(buffer.mapState, "pending");
  callbacks.completeNext();
  await second;
  assert.equal(buffer.mapState, "mapped");
  assert.equal(allocation(host, buffer.allocationId).status, "live");
});

test("map requests capture mode, range, promise generation, and device timeline", async () => {
  const { callbacks, device } = makeHost();
  const buffer = writeBuffer(device);
  const done = buffer.mapAsync(GPUMapMode.WRITE, 8, 16);
  const request = buffer.pendingMapRequest;
  assert.ok(request);
  assert.equal(request.mode, GPUMapMode.WRITE);
  assert.equal(request.offset, 8);
  assert.equal(request.size, 16);
  assert.equal(request.submittedDeviceEpoch, device.epoch);
  assert.equal(request.submittedNavigationEpoch, 0);
  assert.equal(request.submittedPolicyEpoch, 0);
  assert.ok(request.submittedAtTimeline <= device.timeline);
  const firstGeneration = request.promiseGeneration;
  callbacks.completeNext();
  await done;
  buffer.unmap();

  const secondDone = buffer.mapAsync(GPUMapMode.WRITE, 24, 8);
  const secondRequest = buffer.pendingMapRequest;
  assert.ok(secondRequest);
  assert.ok(secondRequest.promiseGeneration > firstGeneration);
  assert.ok(secondRequest.submittedAtTimeline > request.submittedAtTimeline);
  callbacks.completeNext();
  await secondDone;
  buffer.unmap();
});

test("cross-origin hosts have separate generation and promise spaces", async () => {
  const { callbacks: callbacksA, device: deviceA } = makeHost("https://a.example");
  const { host: hostB, callbacks: callbacksB, device: deviceB } = makeHost(
    "https://b.example",
  );
  const bufferA = writeBuffer(deviceA);
  const bufferB = writeBuffer(deviceB);
  assert.equal(bufferA.slot, 0);
  assert.equal(bufferB.slot, 0);
  assert.equal(bufferA.generation, 1);
  assert.equal(bufferB.generation, 1);
  assert.notEqual(bufferA.identity.origin, bufferB.identity.origin);
  assert.notEqual(bufferA.identity.deviceId, bufferB.identity.deviceId);

  assert.throws(
    () => hostB.createBuffer(deviceA, { size: 32, usage: U.MAP_WRITE | U.COPY_SRC }),
    (error) => error instanceof BufferMappingError && error.code === "InvalidStateError",
  );

  bufferA.destroy();
  const bufferA2 = writeBuffer(deviceA);
  assert.equal(bufferA2.slot, 0);
  assert.equal(bufferA2.generation, 2);
  assert.equal(bufferB.generation, 1);

  const mapA = bufferA2.mapAsync(GPUMapMode.WRITE, 0, 64);
  const mapB = bufferB.mapAsync(GPUMapMode.WRITE, 0, 64);
  assert.equal(bufferA2.pendingMapRequest?.promiseGeneration, 1);
  assert.equal(bufferB.pendingMapRequest?.promiseGeneration, 1);
  callbacksA.completeNext();
  callbacksB.completeNext();
  await Promise.all([mapA, mapB]);
  bufferA2.unmap();
  bufferB.unmap();

  bufferB.destroy();
  const bufferB2 = writeBuffer(deviceB);
  assert.equal(bufferB2.generation, 2);
  assert.equal(bufferA2.generation, 2);
});

test("independently constructed hosts have non-colliding partition identities", () => {
  const first = makeHost("https://same.example");
  const second = makeHost("https://same.example");
  const firstBuffer = writeBuffer(first.device);
  const secondBuffer = writeBuffer(second.device);
  assert.equal(firstBuffer.generation, secondBuffer.generation);
  assert.notEqual(first.host.partitionId, second.host.partitionId);
  assert.notEqual(firstBuffer.identity.partitionId, secondBuffer.identity.partitionId);
  assert.notEqual(firstBuffer.identity.deviceId, secondBuffer.identity.deviceId);
  assert.notEqual(firstBuffer.allocationId, secondBuffer.allocationId);
});
