import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import assert from "node:assert/strict";
import { allCells } from "../src/axial.ts";
import { auditStore } from "../src/audit.ts";
import { CrashFault, LeaseStore } from "../src/store.ts";
import { ReferenceStore } from "../src/reference.ts";
import { ERR } from "../src/model.ts";
import type { OpResult } from "../src/model.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const RADIUS = 2;
const CELLS = allCells(RADIUS);
const TENANTS = ["t1", "t2", "t3"];
const LEASE_IDS = ["L1", "L2", "L3", "L4", "L5", "L6"];

interface FaultState {
  failOnPartitionWrite: number | null; // throw a regular error on the Nth partition write
  crashBeforeCommit: boolean;
  writesSeen: number;
}

function runSeed(seed: number, steps: number): void {
  const rand = mulberry32(seed);
  const pick = <T>(xs: T[]): T => xs[Math.floor(rand() * xs.length)];
  const randInt = (n: number) => Math.floor(rand() * n);
  const randCells = () => {
    const n = 1 + randInt(4);
    const shuffled = [...CELLS].sort(() => rand() - 0.5);
    return shuffled.slice(0, n);
  };

  const dir = mkdtempSync(join(tmpdir(), "hexlease-diff-"));
  const meta = { radius: RADIUS, defaultCapacity: 1, numPartitions: 3 };
  LeaseStore.init(dir, meta);
  const fault: FaultState = { failOnPartitionWrite: null, crashBeforeCommit: false, writesSeen: 0 };
  const hooks = {
    beforePartitionWrite: () => {
      fault.writesSeen++;
      if (fault.failOnPartitionWrite !== null && fault.writesSeen === fault.failOnPartitionWrite) {
        throw new Error("injected partition write failure");
      }
    },
    beforeCommit: () => {
      if (fault.crashBeforeCommit) throw new CrashFault();
    },
  };
  let store = LeaseStore.open(dir, hooks);
  const ref = new ReferenceStore(RADIUS, 1);
  const ownership = new Map<string, number>();
  const history: { requestId: string; kind: string; args: Record<string, unknown> }[] = [];

  const compareDumps = (ctx: string) => {
    const expected = ref.dump();
    (expected as Record<string, unknown>).ownership = [...ownership.entries()].sort(([a], [b]) =>
      a < b ? -1 : 1,
    );
    assert.deepStrictEqual(store.dump(), expected, `dump mismatch at ${ctx}`);
  };

  const armFaults = () => {
    fault.failOnPartitionWrite = null;
    fault.crashBeforeCommit = false;
    fault.writesSeen = 0;
    const roll = rand();
    if (roll < 0.04) fault.failOnPartitionWrite = 1 + randInt(2);
    else if (roll < 0.06) fault.crashBeforeCommit = true;
  };

  const applyBoth = (
    kind: string,
    args: Record<string, unknown>,
    tick: number,
    step: number,
  ) => {
    let result: OpResult;
    try {
      result = (store as unknown as Record<string, (a: unknown, t: number) => OpResult>)[kind](
        args,
        tick,
      );
    } catch (err) {
      if (err instanceof CrashFault) {
        // Simulated crash before the commit record: reopen and continue.
        store = LeaseStore.open(dir, hooks);
        compareDumps(`step ${step} after crash-recovery`);
        return;
      }
      throw err;
    }
    if (result.error === ERR.TX_ABORTED) {
      compareDumps(`step ${step} after abort`);
      return; // aborted: nothing applied, request id not consumed
    }
    const refResult = (ref as unknown as Record<string, (a: unknown, t: number) => OpResult>)[
      kind
    ](args, tick);
    assert.deepStrictEqual(result, refResult, `result mismatch at step ${step} (${kind})`);
    compareDumps(`step ${step}`);
  };

  for (let step = 0; step < steps; step++) {
    armFaults();
    const tick = store.currentTick + randInt(3);
    const kind = rand();
    if (kind < 0.12) {
      // explicit tick advance
      let r1: OpResult;
      try {
        r1 = store.tick(tick);
      } catch (err) {
        if (err instanceof CrashFault) {
          store = LeaseStore.open(dir, hooks);
          compareDumps(`step ${step} tick after crash-recovery`);
          continue;
        }
        throw err;
      }
      if (r1.error === ERR.TX_ABORTED) {
        compareDumps(`step ${step} tick after abort`);
        continue;
      }
      const r2 = ref.tick(tick);
      assert.deepStrictEqual(r1, r2, `tick mismatch at step ${step}`);
      compareDumps(`step ${step} tick`);
    } else if (kind < 0.4) {
      // create
      const reuse = history.length > 0 && rand() < 0.2 ? pick(history) : null;
      let args;
      if (reuse && reuse.kind === "create" && rand() < 0.5) {
        args = reuse.args; // idempotent replay
      } else if (reuse) {
        args = { ...reuse.args, cells: randCells() }; // conflicting content
      } else {
        const expires =
          rand() < 0.55 ? null : rand() < 0.85 ? tick + 1 + randInt(5) : Math.max(0, tick - 1);
        args = {
          tenantId: pick(TENANTS),
          leaseId: pick(LEASE_IDS),
          cells: randCells(),
          expiresAtTick: expires,
          requestId: `req-${step}`,
        };
        history.push({ requestId: args.requestId, kind: "create", args });
      }
      applyBoth("create", args, tick, step);
    } else if (kind < 0.62) {
      // replace: mostly well-formed against live leases
      const leases = (store.dump() as { leases: { leaseId: string; tenantId: string; version: number }[] })
        .leases;
      const live = leases.length > 0 && rand() < 0.8 ? pick(leases) : null;
      const args = {
        tenantId: live ? live.tenantId : pick(TENANTS),
        leaseId: live ? live.leaseId : pick(LEASE_IDS),
        expectedVersion: live ? live.version + (rand() < 0.15 ? 1 : 0) : 1,
        cells: randCells(),
        expiresAtTick: rand() < 0.6 ? null : tick + 1 + randInt(5),
        requestId: `req-${step}`,
      };
      history.push({ requestId: args.requestId, kind: "replace", args });
      applyBoth("replace", args, tick, step);
    } else if (kind < 0.72) {
      // release
      const leases = (store.dump() as { leases: { leaseId: string; tenantId: string; version: number }[] })
        .leases;
      const live = leases.length > 0 && rand() < 0.8 ? pick(leases) : null;
      const args = {
        tenantId: live ? live.tenantId : pick(TENANTS),
        leaseId: live ? live.leaseId : pick(LEASE_IDS),
        expectedVersion: live ? live.version : 1,
        requestId: `req-${step}`,
      };
      history.push({ requestId: args.requestId, kind: "release", args });
      applyBoth("release", args, tick, step);
    } else if (kind < 0.82) {
      applyBoth("setCapacity", { cell: pick(CELLS), capacity: randInt(4) }, tick, step);
    } else if (kind < 0.9) {
      applyBoth(
        "setQuota",
        { tenantId: pick(TENANTS), quota: rand() < 0.2 ? null : randInt(9) },
        tick,
        step,
      );
    } else {
      // reassign ownership (store-only concern; tracked separately).
      // Use the current tick so the reference clock stays in sync.
      const cell = pick(CELLS);
      const partition = randInt(meta.numPartitions);
      let r: OpResult;
      try {
        r = store.reassign({ cell, partition }, store.currentTick);
      } catch (err) {
        if (err instanceof CrashFault) {
          store = LeaseStore.open(dir, hooks);
          compareDumps(`step ${step} reassign after crash-recovery`);
          continue;
        }
        throw err;
      }
      if (r.error === ERR.TX_ABORTED) {
        compareDumps(`step ${step} reassign after abort`);
        continue;
      }
      assert.equal(r.ok, true);
      ownership.set(cell, partition);
      compareDumps(`step ${step} reassign`);
    }

    // Periodic restart: recovery must reproduce the exact state.
    if (step % 40 === 39) {
      store.close();
      store = LeaseStore.open(dir, hooks);
      compareDumps(`step ${step} after restart`);
      const report = auditStore(dir);
      assert.ok(
        report.ok,
        `audit failed at step ${step}: ${[...report.grantViolations, ...report.partitionErrors, ...report.sweepErrors].join("; ")}`,
      );
    }
  }

  store.close();
  const final = auditStore(dir);
  assert.ok(final.ok, "final audit must prove no over-allocation");
}

test("differential: journaled store matches serial reference (seed 7)", () => {
  runSeed(7, 300);
});

test("differential: journaled store matches serial reference (seed 42)", () => {
  runSeed(42, 300);
});

test("differential: journaled store matches serial reference (seed 1337)", () => {
  runSeed(1337, 300);
});
