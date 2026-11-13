import assert from "node:assert/strict";
import test from "node:test";

import {
  LockMode,
  ManualClock,
  WebLockManager,
  bytesToHex,
  createLockIdentity,
  decodeLockName,
  lockIdentityKey,
  lockNameBytes,
  type LockRequestOptions,
} from "../index.ts";

const ORIGIN = "https://app.example.test";

function request(
  overrides: Partial<LockRequestOptions>,
): LockRequestOptions {
  return {
    storagePartition: "site-a",
    origin: ORIGIN,
    lockName: "secret",
    mode: LockMode.Exclusive,
    documentGeneration: 1,
    requestID: "request",
    ...overrides,
  };
}

test("identity keys are injective across adversarial delimiters and raw bytes", () => {
  // These two tuples collide under the common naive [p, o, name].join("|")
  // encoding, but not under the structured identity key.
  const left = ["top|a", "o", "n", 0] as const;
  const right = ["top", "a|o", "n", 0] as const;
  assert.equal(
    [left[0], left[1], left[2]].join("|"),
    [right[0], right[1], right[2]].join("|"),
  );
  assert.notEqual(
    lockIdentityKey(left[0], left[1], left[2], left[3]),
    lockIdentityKey(right[0], right[1], right[2], right[3]),
  );

  const rawA = new Uint8Array([0x73, 0x65, 0x63, 0x72, 0x65, 0x74, 0xff]);
  const rawB = new Uint8Array([0x73, 0x65, 0x63, 0x72, 0x65, 0x74, 0xfe]);
  assert.equal(decodeLockName(rawA), undefined);
  assert.notEqual(
    lockIdentityKey("site-a", ORIGIN, rawA, 0),
    lockIdentityKey("site-a", ORIGIN, rawB, 0),
  );
  assert.equal(bytesToHex(rawA), "736563726574ff");

  // A string and its exact UTF-8 bytes are the same lock name.
  assert.equal(
    lockIdentityKey("site-a", ORIGIN, "caf\u00e9", 0),
    lockIdentityKey(
      "site-a",
      ORIGIN,
      lockNameBytes("caf\u00e9"),
      0,
    ),
  );

  // NUL and quote-heavy names stay inside their tuple component.
  assert.notEqual(
    lockIdentityKey("site-a", ORIGIN, "a\u0000b", 0),
    lockIdentityKey("site-a", ORIGIN, "a", 0),
  );
  assert.notEqual(
    lockIdentityKey('site"a', ORIGIN, "lock", 0),
    lockIdentityKey("site", 'a"' + ORIGIN, "lock", 0),
  );

  // Generation is part of the identity, not just queue metadata.
  assert.notEqual(
    lockIdentityKey("site-a", ORIGIN, "secret", 0),
    lockIdentityKey("site-a", ORIGIN, "secret", 1),
  );
});

test("an adversarial delimiter collision does not create cross-partition blocking", async () => {
  const manager = new WebLockManager({ clock: new ManualClock(0) });
  const lockNameA = "n";
  const lockNameB = "n";
  const tupleA = {
    storagePartition: "top|a",
    origin: "o",
  };
  const tupleB = {
    storagePartition: "top",
    origin: "a|o",
  };

  const heldA = await manager.request(
    request({
      storagePartition: tupleA.storagePartition,
      origin: tupleA.origin,
      lockName: lockNameA,
      requestID: "tuple-a",
    }),
  );
  const heldB = await manager.request(
    request({
      storagePartition: tupleB.storagePartition,
      origin: tupleB.origin,
      lockName: lockNameB,
      requestID: "tuple-b",
    }),
  );
  assert.ok(heldA);
  assert.ok(heldB);
  assert.equal(heldB.released, false);

  const queryA = manager.query({
    storagePartition: tupleA.storagePartition,
    origin: tupleA.origin,
  });
  const queryB = manager.query({
    storagePartition: tupleB.storagePartition,
    origin: tupleB.origin,
  });
  assert.deepEqual(
    queryA.held.map((lock) => lock.requestID),
    ["tuple-a"],
  );
  assert.deepEqual(
    queryB.held.map((lock) => lock.requestID),
    ["tuple-b"],
  );

  heldA.release();
  heldB.release();
});

test("pending callback registries do not collide across partitions", async () => {
  const manager = new WebLockManager({ clock: new ManualClock(0) });
  const starts: string[] = [];
  const finish = new Map<string, () => void>();

  const leaseA = await manager.request(
    request({
      storagePartition: "site-a",
      requestID: "a-holder",
      documentGeneration: 1,
    }),
  );
  const leaseB = await manager.request(
    request({
      storagePartition: "site-b",
      requestID: "b-holder",
      documentGeneration: 2,
    }),
  );
  assert.ok(leaseA);
  assert.ok(leaseB);

  const callbackRequest = (
    partition: string,
    id: string,
    documentGeneration: number,
  ): Promise<string | null> =>
    manager.request(
      request({
        storagePartition: partition,
        requestID: id,
        documentGeneration,
      }),
      () =>
        new Promise<string>((resolve) => {
          starts.push(id);
          finish.set(id, () => resolve(id));
        }),
    );

  const pendingA = callbackRequest("site-a", "a-pending", 1);
  const pendingB = callbackRequest("site-b", "b-pending", 2);
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.deepEqual(starts, []);

  leaseA.release();
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.deepEqual(starts, ["a-pending"]);

  finish.get("a-pending")!();
  await pendingA;
  assert.deepEqual(starts, ["a-pending"]);

  leaseB.release();
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.deepEqual(starts, ["a-pending", "b-pending"]);
  finish.get("b-pending")!();
  await pendingB;
});

test("same-name activity in another partition cannot be observed through ifAvailable timing", async () => {
  const baselineClock = new ManualClock(1_000);
  const baseline = new WebLockManager({ clock: baselineClock });
  const baselineTimeline: string[] = [];

  const baselineProbe = await baseline.request(
    request({
      storagePartition: "site-b",
      requestID: "b-probe",
      ifAvailable: true,
      documentGeneration: 2,
    }),
  );
  baselineTimeline.push(`probe:${baselineProbe ? "acquired" : "miss"}`);
  assert.ok(baselineProbe);
  baselineProbe.release();
  baselineTimeline.push(`released:${baselineClock.now()}`);

  const observedClock = new ManualClock(1_000);
  const observed = new WebLockManager({ clock: observedClock });
  const observedTimeline: string[] = [];

  const hiddenA = await observed.request(
    request({
      storagePartition: "site-a",
      requestID: "a-held",
      documentGeneration: 1,
    }),
  );
  assert.ok(hiddenA);

  const observedProbe = await observed.request(
    request({
      storagePartition: "site-b",
      requestID: "b-probe",
      ifAvailable: true,
      documentGeneration: 2,
    }),
  );
  observedTimeline.push(`probe:${observedProbe ? "acquired" : "miss"}`);
  assert.ok(observedProbe);
  assert.equal(observedProbe.released, false);
  observedProbe.release();
  observedTimeline.push(`released:${observedClock.now()}`);

  assert.deepEqual(observedTimeline, baselineTimeline);
  hiddenA.release();
});

test("shortest collision trace search finds no cross-partition block", async () => {
  const partitions = ["top|a", "top", "top|a|b", "top\u0000a"];
  const origins = ["o", "a|o", "a|b|o", "o\u0000a"];
  const names = ["n", "|n", "n|", "\u0000n"];
  const generations = [0, 1];

  const seen = new Map<string, string>();
  for (const partition of partitions) {
    for (const origin of origins) {
      for (const name of names) {
        for (const generation of generations) {
          const key = lockIdentityKey(partition, origin, name, generation);
          const tuple = JSON.stringify([partition, origin, name, generation]);
          const previous = seen.get(key);
          assert.equal(
            previous,
            undefined,
            `collision between ${previous} and ${tuple}`,
          );
          seen.set(key, tuple);
        }
      }
    }
  }

  // Exhaust the three shortest bounded traces in which A holds an Exclusive
  // lock while B probes/acquires the same name. None can leave B pending.
  const manager = new WebLockManager({ clock: new ManualClock(0) });
  const aLease = await manager.request(
    request({
      storagePartition: partitions[0],
      origin: origins[0],
      lockName: names[0],
      documentGeneration: 1,
      requestID: "a",
    }),
  );
  assert.ok(aLease);

  for (const [index, mode] of [
    LockMode.Shared,
    LockMode.Exclusive,
  ].entries()) {
    const bLease = await manager.request(
      request({
        storagePartition: partitions[1],
        origin: origins[1],
        lockName: names[0],
        mode,
        documentGeneration: 2,
        requestID: `b-${index}`,
      }),
    );
    assert.ok(bLease, "trace length 1 must not block B");
    assert.deepEqual(
      manager.query({
        storagePartition: partitions[1],
        origin: origins[1],
      }).pending,
      [],
    );
    bLease.release();
  }

  const probe = await manager.request(
    request({
      storagePartition: partitions[1],
      origin: origins[1],
      lockName: names[0],
      ifAvailable: true,
      documentGeneration: 2,
      requestID: "b-probe",
    }),
  );
  assert.ok(probe);
  probe.release();
  aLease.release();
});

test("identity copies do not let a caller mutate the queue key", async () => {
  const manager = new WebLockManager({ clock: new ManualClock(0) });
  const name = new Uint8Array([0x61, 0x62, 0x63]);
  const identity = createLockIdentity("site-a", ORIGIN, name, 0);
  name[0] = 0x7a;
  assert.equal(bytesToHex(identity.lockName), "616263");

  const lease = await manager.request(
    request({
      storagePartition: "site-a",
      origin: ORIGIN,
      lockName: identity.lockName,
      requestID: "copy",
    }),
  );
  assert.ok(lease);
  const leaseCopy = lease.lockName;
  leaseCopy[0] = 0x00;
  assert.equal(bytesToHex(lease.lockName), "616263");
  assert.equal(bytesToHex(lease.identity.lockName), "616263");
  assert.equal(Object.isFrozen(lease), true);
  lease.release();
});
