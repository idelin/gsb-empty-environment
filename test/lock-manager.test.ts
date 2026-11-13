import assert from "node:assert/strict";
import test from "node:test";

import {
  LockMode,
  LockNavigationError,
  LockPolicyError,
  LockTimeoutError,
  ManualClock,
  WebLockManager,
  type LockRequestOptions,
  type LockView,
  type WebLockLease,
} from "../index.ts";

const PARTITION_A = "top-level-site-a";
const PARTITION_B = "top-level-site-b";
const ORIGIN = "https://app.example.test";

function options(
  overrides: Partial<LockRequestOptions> = {},
): LockRequestOptions {
  return {
    storagePartition: PARTITION_A,
    origin: ORIGIN,
    lockName: "ledger",
    mode: LockMode.Exclusive,
    documentGeneration: 1,
    requestID: "request-1",
    ...overrides,
  };
}

async function flushMicrotasks(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
  await new Promise<void>((resolve) => setImmediate(resolve));
}

function deferred<T = void>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason?: unknown) => void;
} {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

test("same lock name in different top-level partitions never blocks", async () => {
  const clock = new ManualClock(1_000);
  const manager = new WebLockManager({ clock });

  const leaseA = await manager.request(
    options({
      storagePartition: PARTITION_A,
      documentGeneration: 11,
      requestID: "a-held",
    }),
  );
  assert.ok(leaseA);

  const leaseB = await manager.request(
    options({
      storagePartition: PARTITION_B,
      documentGeneration: 22,
      requestID: "b-held",
    }),
  );
  assert.ok(leaseB);
  assert.equal(clock.now(), 1_000, "B must not wait for A");
  assert.notEqual(leaseA.leaseID, leaseB.leaseID);

  const queryA = manager.query({
    storagePartition: PARTITION_A,
    origin: ORIGIN,
  });
  const queryB = manager.query({
    storagePartition: PARTITION_B,
    origin: ORIGIN,
  });

  assert.deepEqual(
    queryA.held.map((lock) => lock.requestID),
    ["a-held"],
  );
  assert.deepEqual(
    queryB.held.map((lock) => lock.requestID),
    ["b-held"],
  );

  leaseA.release();
  leaseB.release();
});

test("FIFO queue grants a contiguous Shared batch without passing Exclusive", async () => {
  const manager = new WebLockManager({ clock: new ManualClock(0) });
  const holder = await manager.request(
    options({ requestID: "holder", documentGeneration: 1 }),
  );
  assert.ok(holder);

  const started: string[] = [];
  const leases = new Map<string, WebLockLease>();
  const finishes = new Map<string, (value: string) => void>();

  const enqueue = (id: string, mode: LockMode): Promise<string | null> =>
    manager.request(
      options({
        requestID: id,
        documentGeneration: 2,
        mode,
      }),
      (lease) => {
        started.push(id);
        leases.set(id, lease);
        return new Promise<string>((resolve) => {
          finishes.set(id, resolve);
        });
      },
    );

  const requests = [
    enqueue("shared-1", LockMode.Shared),
    enqueue("shared-2", LockMode.Shared),
    enqueue("exclusive-3", LockMode.Exclusive),
    enqueue("shared-4", LockMode.Shared),
    enqueue("shared-5", LockMode.Shared),
  ];

  await flushMicrotasks();
  assert.deepEqual(started, []);

  holder.release();
  await flushMicrotasks();
  assert.deepEqual(
    started,
    ["shared-1", "shared-2"],
    "the leading shared run is one batch",
  );

  const pending = manager.query({
    storagePartition: PARTITION_A,
    origin: ORIGIN,
  }).pending;
  assert.deepEqual(
    pending.map((lock) => lock.requestID),
    ["exclusive-3", "shared-4", "shared-5"],
  );

  leases.get("shared-1")!.release();
  await flushMicrotasks();
  assert.deepEqual(started, ["shared-1", "shared-2"]);

  leases.get("shared-2")!.release();
  await flushMicrotasks();
  assert.deepEqual(
    started,
    ["shared-1", "shared-2", "exclusive-3"],
    "Exclusive cannot pass an earlier Shared holder",
  );

  leases.get("exclusive-3")!.release();
  await flushMicrotasks();
  assert.deepEqual(
    started,
    ["shared-1", "shared-2", "exclusive-3", "shared-4", "shared-5"],
    "the trailing shared run is granted as a batch",
  );

  for (const id of ["shared-1", "shared-2", "exclusive-3", "shared-4", "shared-5"]) {
    finishes.get(id)?.("done");
  }
  await Promise.all(requests);
});

test("navigation releases old leases and a late callback cannot release a newer lease", async () => {
  const manager = new WebLockManager({ clock: new ManualClock(0) });
  const callbackGate = deferred();
  let oldLease: WebLockLease | undefined;

  const oldRequest = manager.request(
    options({
      storagePartition: PARTITION_A,
      documentGeneration: 10,
      requestID: "old-document",
    }),
    async (lease) => {
      oldLease = lease;
      await callbackGate.promise;
      // Deliberately use the stale lease after the document was navigated.
      lease.release();
      return "old-result";
    },
  );
  const oldRejected = oldRequest.then(
    () => {
      throw new Error("old request should have been rejected");
    },
    (error: unknown) => error,
  );

  await flushMicrotasks();
  assert.ok(oldLease);

  const newRequest = manager.request(
    options({
      storagePartition: PARTITION_A,
      documentGeneration: 11,
      requestID: "new-document",
    }),
  );
  await flushMicrotasks();

  const released = manager.releaseDocument({
    storagePartition: PARTITION_A,
    origin: ORIGIN,
    documentGeneration: 10,
  });
  assert.deepEqual(released, { pendingRejected: 0, leasesReleased: 1 });

  const newLease = await newRequest;
  assert.ok(newLease);
  assert.equal(oldLease.released, true);
  assert.equal(newLease.released, false);
  assert.notEqual(oldLease.leaseID, newLease.leaseID);

  const navigationError = await oldRejected;
  assert.ok(navigationError instanceof LockNavigationError);
  assert.equal((navigationError as Error).name, "NavigationError");

  callbackGate.resolve();
  await flushMicrotasks();

  assert.equal(newLease.released, false, "late old callback must not release new lease");
  assert.equal(
    manager.query({
      storagePartition: PARTITION_A,
      origin: ORIGIN,
    }).held.length,
    1,
  );

  oldLease.release();
  assert.equal(newLease.released, false);
  newLease.release();
});

test("abort wins when it fires before a queued grant and after a synchronous grant", async () => {
  const manager = new WebLockManager({ clock: new ManualClock(0) });
  const blocker = await manager.request(
    options({ requestID: "blocker", mode: LockMode.Exclusive }),
  );
  assert.ok(blocker);

  const firstController = new AbortController();
  let firstStarted = false;
  const first = manager.request(
    options({
      requestID: "abort-before-grant",
      mode: LockMode.Shared,
      documentGeneration: 2,
      signal: firstController.signal,
    }),
    () => {
      firstStarted = true;
      return "unexpected";
    },
  );
  await flushMicrotasks();
  firstController.abort();

  await assert.rejects(first, (error: unknown) => {
    return error instanceof Error && error.name === "AbortError";
  });
  blocker.release();
  await flushMicrotasks();
  assert.equal(firstStarted, false);
  assert.deepEqual(
    manager.query({
      storagePartition: PARTITION_A,
      origin: ORIGIN,
    }).pending,
    [],
  );

  const secondBlocker = await manager.request(
    options({ requestID: "blocker-2", mode: LockMode.Exclusive }),
  );
  assert.ok(secondBlocker);
  const secondController = new AbortController();
  let secondStarted = false;
  const second = manager.request(
    options({
      requestID: "abort-after-grant",
      mode: LockMode.Exclusive,
      documentGeneration: 3,
      signal: secondController.signal,
    }),
    () => {
      secondStarted = true;
      return "unexpected";
    },
  );

  // Dispatch is synchronous: this grants the waiter before the abort event.
  secondBlocker.release();
  secondController.abort();

  await assert.rejects(second, (error: unknown) => {
    return error instanceof Error && error.name === "AbortError";
  });
  await flushMicrotasks();
  assert.equal(secondStarted, false, "abort settles the request before its callback starts");
  assert.deepEqual(
    manager.query({
      storagePartition: PARTITION_A,
      origin: ORIGIN,
    }),
    { held: [], pending: [] },
  );
});

test("ifAvailable is a non-queueing, partition-local probe", async () => {
  const manager = new WebLockManager({ clock: new ManualClock(50) });
  const heldA = await manager.request(
    options({
      storagePartition: PARTITION_A,
      documentGeneration: 1,
      requestID: "held-a",
    }),
  );
  assert.ok(heldA);

  let callbackCalled = false;
  const unavailable = manager.request(
    options({
      storagePartition: PARTITION_A,
      documentGeneration: 2,
      requestID: "if-available-conflict",
      ifAvailable: true,
    }),
    () => {
      callbackCalled = true;
      return "unexpected";
    },
  );
  assert.equal(await unavailable, null);
  assert.equal(callbackCalled, false);
  assert.deepEqual(
    manager.query({
      storagePartition: PARTITION_A,
      origin: ORIGIN,
    }).pending,
    [],
    "ifAvailable conflict is not queued",
  );

  const leaseB = await manager.request(
    options({
      storagePartition: PARTITION_B,
      documentGeneration: 2,
      requestID: "if-available-other-partition",
      ifAvailable: true,
    }),
  );
  assert.ok(leaseB);
  assert.equal(leaseB.released, false);

  heldA.release();
  leaseB.release();
});

test("deadline rejects pending work and releases granted leases", async () => {
  const clock = new ManualClock(100);
  const manager = new WebLockManager({ clock });
  const blocker = await manager.request(
    options({ requestID: "deadline-blocker" }),
  );
  assert.ok(blocker);

  const pending = manager.request(
    options({
      requestID: "deadline-pending",
      documentGeneration: 2,
      mode: LockMode.Shared,
      deadline: 110,
    }),
  );
  const pendingRejected = pending.then(
    () => {
      throw new Error("pending request should time out");
    },
    (error: unknown) => error,
  );

  clock.advance(10);
  const error = await pendingRejected;
  assert.ok(error instanceof LockTimeoutError);
  assert.equal(clock.now(), 110);
  assert.deepEqual(
    manager.query({
      storagePartition: PARTITION_A,
      origin: ORIGIN,
    }).pending,
    [],
  );
  blocker.release();

  const granted = await manager.request(
    options({
      requestID: "deadline-granted",
      documentGeneration: 3,
      deadline: 120,
    }),
  );
  assert.ok(granted);
  clock.advance(10);
  assert.equal(granted.released, true);
  assert.equal(granted.releaseInfo?.reason, "deadline");
  assert.deepEqual(
    manager.query({
      storagePartition: PARTITION_A,
      origin: ORIGIN,
    }),
    { held: [], pending: [] },
  );
});

test("policy generation rejects old pending requests and revokes old grants", async () => {
  const manager = new WebLockManager({ clock: new ManualClock(0) });
  const oldLease = await manager.request(
    options({
      requestID: "old-holder",
      documentGeneration: 1,
      hostPolicyGeneration: 0,
    }),
  );
  assert.ok(oldLease);

  const pending = manager.request(
    options({
      requestID: "old-pending",
      documentGeneration: 2,
    }),
  );
  const pendingOutcome = pending.then(
    () => {
      throw new Error("old pending request should be rejected");
    },
    (error: unknown) => error,
  );
  await flushMicrotasks();

  const changed = manager.setHostPolicyGeneration(PARTITION_A, 1);
  assert.deepEqual(changed, {
    storagePartition: PARTITION_A,
    previousGeneration: 0,
    generation: 1,
    pendingRejected: 1,
    leasesRevoked: 1,
  });
  assert.equal(oldLease.released, true);
  assert.equal(oldLease.releaseInfo?.reason, "policy");

  const policyError = await pendingOutcome;
  assert.ok(policyError instanceof LockPolicyError);
  assert.equal((policyError as Error).name, "PolicyError");

  await assert.rejects(
    manager.request(
      options({
        requestID: "stale-explicit-generation",
        hostPolicyGeneration: 0,
      }),
    ),
    (error: unknown) => error instanceof LockPolicyError,
  );

  const newLease = await manager.request(
    options({
      requestID: "new-generation-holder",
      documentGeneration: 3,
      hostPolicyGeneration: 1,
    }),
  );
  assert.ok(newLease);
  assert.equal(newLease.identity.hostPolicyGeneration, 1);
  assert.equal(newLease.released, false);
  newLease.release();

  // A stale lease may call release after reuse; it must be a no-op.
  oldLease.release();
  assert.equal(newLease.released, true);
});

test("query is partition/origin scoped and honors document visibility", async () => {
  const manager = new WebLockManager({
    clock: new ManualClock(0),
    canViewLock: (viewer, lock): boolean =>
      viewer.documentGeneration === lock.documentGeneration,
  });

  const docOne = await manager.request(
    options({
      requestID: "doc-1",
      documentGeneration: 1,
      mode: LockMode.Shared,
    }),
  );
  const docTwo = await manager.request(
    options({
      requestID: "doc-2",
      documentGeneration: 2,
      mode: LockMode.Shared,
    }),
  );
  assert.ok(docOne);
  assert.ok(docTwo);

  const otherOrigin = await manager.request(
    options({
      requestID: "other-origin",
      origin: "https://other.example.test",
      documentGeneration: 1,
    }),
  );
  const otherPartition = await manager.request(
    options({
      requestID: "other-partition",
      storagePartition: PARTITION_B,
      documentGeneration: 1,
    }),
  );
  assert.ok(otherOrigin);
  assert.ok(otherPartition);

  const viewOne = manager.query({
    storagePartition: PARTITION_A,
    origin: ORIGIN,
    documentGeneration: 1,
  });
  const viewTwo = manager.query({
    storagePartition: PARTITION_A,
    origin: ORIGIN,
    documentGeneration: 2,
  });
  const viewOtherPartition = manager.query({
    storagePartition: PARTITION_B,
    origin: ORIGIN,
    documentGeneration: 1,
  });

  assert.deepEqual(
    viewOne.held.map((lock: LockView) => lock.requestID),
    ["doc-1"],
  );
  assert.deepEqual(
    viewTwo.held.map((lock: LockView) => lock.requestID),
    ["doc-2"],
  );
  assert.deepEqual(
    viewOtherPartition.held.map((lock: LockView) => lock.requestID),
    ["other-partition"],
  );
  assert.ok(
    viewOne.held.every(
      (lock) =>
        lock.storagePartition === PARTITION_A && lock.origin === ORIGIN,
    ),
  );

  docOne.release();
  docTwo.release();
  otherOrigin.release();
  otherPartition.release();
});

test("query defaults to the caller document when no host permission hook exists", async () => {
  const manager = new WebLockManager({ clock: new ManualClock(0) });
  const docOne = await manager.request(
    options({
      requestID: "doc-only-1",
      documentGeneration: 1,
      mode: LockMode.Shared,
    }),
  );
  const docTwo = await manager.request(
    options({
      requestID: "doc-only-2",
      documentGeneration: 2,
      mode: LockMode.Shared,
    }),
  );
  assert.ok(docOne);
  assert.ok(docTwo);

  const queryOne = manager.query({
    storagePartition: PARTITION_A,
    origin: ORIGIN,
    documentGeneration: 1,
  });
  assert.deepEqual(
    queryOne.held.map((lock) => lock.requestID),
    ["doc-only-1"],
  );

  const queryTwo = manager.query({
    storagePartition: PARTITION_A,
    origin: ORIGIN,
    documentGeneration: 2,
  });
  assert.deepEqual(
    queryTwo.held.map((lock) => lock.requestID),
    ["doc-only-2"],
  );

  docOne.release();
  docTwo.release();
});

test("lease release is exactly once and lease IDs are not reused", async () => {
  const events: string[] = [];
  const manager = new WebLockManager({
    clock: new ManualClock(5),
    onLeaseReleased: (event) => {
      events.push(event.info.leaseID);
    },
  });

  const first = await manager.request(
    options({ requestID: "first-lease", documentGeneration: 1 }),
  );
  assert.ok(first);
  const firstReleased = first.whenReleased;

  first.release();
  first.release();
  manager.releaseLease(first);

  const info = await firstReleased;
  assert.equal(info.reason, "explicit");
  assert.deepEqual(events, [first.leaseID]);

  const second = await manager.request(
    options({ requestID: "second-lease", documentGeneration: 1 }),
  );
  assert.ok(second);
  assert.notEqual(second.leaseID, first.leaseID);
  second.release();

  assert.equal(events.length, 2);

  const fixedIDs = new WebLockManager({
    clock: new ManualClock(0),
    createLeaseID: () => "fixed-id",
  });
  const fixedFirst = await fixedIDs.request(
    options({ requestID: "fixed-first" }),
  );
  assert.ok(fixedFirst);
  fixedFirst.release();
  const fixedSecond = await fixedIDs.request(
    options({ requestID: "fixed-second" }),
  );
  assert.ok(fixedSecond);
  assert.equal(fixedFirst.leaseID, "fixed-id");
  assert.notEqual(fixedSecond.leaseID, fixedFirst.leaseID);
  fixedSecond.release();
});
