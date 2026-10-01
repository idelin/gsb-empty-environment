import { test } from "node:test";
import assert from "node:assert/strict";
import { ErrorIndex } from "../src/error-index.ts";
import { errorKey, toMultiset, type ValidationError } from "../src/errors.ts";

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

const RULES: { rule: string; message: string; params: Record<string, unknown> }[] = [
  { rule: "minimum", message: "must be >= 1", params: { minimum: 1 } },
  { rule: "type", message: "must be integer", params: { type: "integer" } },
  { rule: "required", message: "must have required property 'name'", params: { missingProperty: "name" } },
];
const VERSIONS = ["v1", "v2"];

function randomError(rand: () => number): ValidationError {
  const r = RULES[Math.floor(rand() * RULES.length)];
  const version = VERSIONS[Math.floor(rand() * VERSIONS.length)];
  const useArrayPath = rand() < 0.8;
  const instancePath = useArrayPath
    ? ["items", Math.floor(rand() * 30), "qty"]
    : ["meta", "label"];
  return {
    schemaVersion: version,
    rule: r.rule,
    instancePath,
    schemaPath: `#/properties/items/items/properties/qty/${r.rule}`,
    message: r.message,
    params: r.params,
  };
}

function multisetOf(errors: readonly ValidationError[]): Map<string, number> {
  return toMultiset(errors);
}

function assertMultisetEqual(
  actual: Map<string, number>,
  expected: Map<string, number>,
): void {
  assert.deepEqual(
    Object.fromEntries([...actual].sort()),
    Object.fromEntries([...expected].sort()),
  );
}

test("differential: index matches an independent reference list under random ops", () => {
  const rand = mulberry32(0xc0ffee);
  const index = new ErrorIndex();
  const reference = new Map<string, number>();
  const live: ValidationError[] = []; // errors currently "present", for realistic retracts

  const refAdd = (err: ValidationError): void => {
    const key = errorKey(err);
    reference.set(key, (reference.get(key) ?? 0) + 1);
    live.push(err);
  };
  const refRemove = (err: ValidationError): void => {
    const key = errorKey(err);
    const count = reference.get(key) ?? 0;
    if (count <= 1) reference.delete(key);
    else reference.set(key, count - 1);
  };

  const OPS = 3000;
  for (let op = 0; op < OPS; op++) {
    const roll = rand();
    if (roll < 0.55 || live.length === 0) {
      const err = randomError(rand);
      index.addError(err);
      refAdd(err);
    } else if (roll < 0.95) {
      const victim = live[Math.floor(rand() * live.length)];
      const ok = index.retractError(victim);
      assert.equal(ok, true, "retract of a live error must succeed");
      refRemove(victim);
      live.splice(live.indexOf(victim), 1);
    } else {
      // retract something that was never added: must fail cleanly
      const ghost = randomError(rand);
      const before = index.totalCount;
      const ok = index.retractError(ghost);
      if (ok) {
        // ghost happened to exist: mirror it in the reference
        refRemove(ghost);
        const at = live.findIndex((e) => errorKey(e) === errorKey(ghost));
        live.splice(at, 1);
      } else {
        assert.equal(index.totalCount, before, "failed retract must not change counts");
      }
    }

    // conservation invariant after every single operation
    const expectedTotal = [...reference.values()].reduce((a, b) => a + b, 0);
    assert.equal(index.totalCount, expectedTotal, `totalCount diverged at op ${op}`);
    assert.equal(
      index.count(),
      index.expand().length,
      `count/expand mismatch at op ${op}`,
    );

    if (op % 250 === 0 || op === OPS - 1) {
      assertMultisetEqual(multisetOf(index.expand()), reference);
      // filtered expansions must also agree with the reference
      for (const rule of RULES.map((r) => r.rule)) {
        const fromIndex = index.expand({ rule });
        const fromRef = live.filter((e) => e.rule === rule);
        assertMultisetEqual(multisetOf(fromIndex), multisetOf(fromRef));
        assert.equal(index.count({ rule }), fromRef.length);
      }
      for (const version of VERSIONS) {
        const fromIndex = index.expand({ schemaVersion: version });
        const fromRef = live.filter((e) => e.schemaVersion === version);
        assertMultisetEqual(multisetOf(fromIndex), multisetOf(fromRef));
      }
      const prefixed = index.expand({ pathPrefix: "/items/3" });
      const prefixedRef = live.filter(
        (e) => e.instancePath[0] === "items" && e.instancePath[1] === 3,
      );
      assertMultisetEqual(multisetOf(prefixed), multisetOf(prefixedRef));
    }
  }

  // serialize/deserialize mid-stream must not disturb conservation
  const restored = ErrorIndex.deserialize(index.serialize());
  assertMultisetEqual(multisetOf(restored.expand()), reference);
  assert.equal(restored.totalCount, index.totalCount);
});
