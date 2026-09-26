import { test } from "node:test";
import assert from "node:assert/strict";
import { DepIndex } from "../src/depindex.ts";
import type { Json, SchemaNode } from "../src/checks.ts";
import { IncrementalValidator, validate } from "../src/validator.ts";

const schema: SchemaNode = {
  type: "object",
  properties: {
    x: { type: "number", minimum: 0 },
    y: { type: "number" },
    z: { type: "string", minLength: 2, maxLength: 5 },
    flag: { type: "boolean" },
    profile: {
      type: "object",
      properties: {
        age: { type: "integer", minimum: 0, maximum: 150 },
        name: { type: "string", pattern: "^[a-z]+$" },
        tags: { type: "array", items: { type: "string", enum: ["a", "b", "c"] } },
      },
      required: ["age"],
    },
  },
  required: ["x", "y"],
  rules: [
    { kind: "lt", left: "/x", right: "/y" },
    { kind: "eq", left: "/x", right: "/profile/age" },
  ],
};

function baseData(): Json {
  return {
    x: 1,
    y: 2,
    z: "ab",
    flag: true,
    profile: { age: 1, name: "abc", tags: ["a", "b"] },
  };
}

test("errors carry both instance path and rule (schema) path", () => {
  const errors = validate(schema, { x: 9, y: 2, z: "toolong!", profile: { age: -1 } });
  const ruleErr = errors.find((e) => e.schemaPath === "#/rules/0");
  assert.ok(ruleErr);
  assert.equal(ruleErr.instancePath, "/x");
  const minErr = errors.find((e) => e.schemaPath === "#/properties/profile/properties/age/minimum");
  assert.ok(minErr);
  assert.equal(minErr.instancePath, "/profile/age");
  for (const e of errors) {
    assert.ok(typeof e.instancePath === "string");
    assert.ok(e.schemaPath.startsWith("#/"));
  }
});

test("changing /x re-validates the x<y rule but not unrelated /z checks", () => {
  const engine = new IncrementalValidator(schema, baseData());
  const paths = engine.checkPaths();
  const zChecks = paths.map((p, i) => [p, i] as const).filter(([p]) => p.startsWith("#/properties/z"));
  const ruleCheck = paths.findIndex((p) => p === "#/rules/0");
  assert.ok(zChecks.length > 0 && ruleCheck >= 0);
  const before = [...engine.evalCounts];

  const result = engine.apply([{ op: "set", path: "/x", value: 10 }]);

  assert.deepEqual(
    result.added.map((e) => e.schemaPath),
    ["#/rules/0", "#/rules/1"],
  );
  assert.equal(result.removed.length, 0);
  assert.ok(engine.evalCounts[ruleCheck] > before[ruleCheck], "rule check re-evaluated");
  for (const [, i] of zChecks) {
    assert.equal(engine.evalCounts[i], before[i], `z check ${paths[i]} must not be re-evaluated`);
  }
  assert.ok(!result.revalidated.some((p) => p.startsWith("#/properties/z")));
});

test("deleting a parent retracts errors reported on its children", () => {
  const engine = new IncrementalValidator(schema, {
    x: 1, y: 2, z: "ab", flag: true,
    profile: { age: -5, name: "OK", tags: ["a", 9] },
  });
  const before = engine.getErrors();
  assert.ok(before.some((e) => e.instancePath === "/profile/age"));
  assert.ok(before.some((e) => e.instancePath === "/profile/name"));
  assert.ok(before.some((e) => e.instancePath === "/profile/tags/1"));

  const result = engine.apply([{ op: "remove", path: "/profile" }]);

  const retracted = result.removed.map((e) => e.instancePath);
  assert.ok(retracted.includes("/profile/age"));
  assert.ok(retracted.includes("/profile/name"));
  assert.ok(retracted.includes("/profile/tags/1"));
  assert.equal(engine.getErrors().filter((e) => e.instancePath.startsWith("/profile")).length, 0);
  assert.deepEqual(engine.getErrors(), validate(schema, engine.getData()));
});

test("removing a required key adds an error; restoring it retracts", () => {
  const engine = new IncrementalValidator(schema, baseData());
  const r1 = engine.apply([{ op: "remove", path: "/x" }]);
  assert.ok(r1.added.some((e) => e.code === "required" && e.instancePath === ""));
  const r2 = engine.apply([{ op: "set", path: "/x", value: 1 }]);
  assert.ok(r2.removed.some((e) => e.code === "required"));
  assert.equal(engine.getErrors().length, 0);
});

test("array element edits and splices keep errors consistent with full validation", () => {
  const engine = new IncrementalValidator(schema, {
    x: 1, y: 2, z: "ab", flag: true,
    profile: { age: 1, tags: ["a", 7, "b", 8] },
  });
  assert.deepEqual(
    engine.getErrors().filter((e) => e.schemaPath === "#/properties/profile/properties/tags/items/type").map((e) => e.instancePath),
    ["/profile/tags/1", "/profile/tags/3"],
  );
  const r = engine.apply([{ op: "remove", path: "/profile/tags/0" }]);
  assert.deepEqual(
    r.removed.filter((e) => e.schemaPath === "#/properties/profile/properties/tags/items/type").map((e) => e.instancePath),
    ["/profile/tags/1", "/profile/tags/3"],
  );
  assert.deepEqual(
    r.added.filter((e) => e.schemaPath === "#/properties/profile/properties/tags/items/type").map((e) => e.instancePath),
    ["/profile/tags/0", "/profile/tags/2"],
  );
  assert.deepEqual(engine.getErrors(), validate(schema, engine.getData()));
});

test("a patch on an unindexed path revalidates nothing", () => {
  const engine = new IncrementalValidator(schema, baseData());
  const result = engine.apply([{ op: "set", path: "/unrelated", value: 123 }]);
  assert.deepEqual(result.revalidated, []);
  assert.deepEqual(result.added, []);
  assert.deepEqual(result.removed, []);
});

test("dependency index: node vs subtree invalidation", () => {
  const index = new DepIndex();
  index.add(["a"], "node", 1);      // reads node /a only
  index.add(["a", "b"], "node", 2); // reads node /a/b
  index.add(["c"], "subtree", 3);   // reads subtree under /c
  assert.deepEqual([...index.query(["a"])].sort(), [1, 2]);       // replace /a
  assert.deepEqual([...index.query(["a", "b"])].sort(), [2]);     // edit below /a
  assert.deepEqual([...index.query(["c", "x", "y"])].sort(), [3]); // deep edit under /c
  assert.deepEqual([...index.query(["elsewhere"])].sort(), []);
});

test("incremental error set equals full revalidation across a random op sequence", () => {
  const rand = mulberry32(20260926);
  const engine = new IncrementalValidator(schema, baseData());
  const values: Json[] = [
    0, 1, 2, 5, -1, 150, 200, 1.5,
    "a", "ab", "abcdef", "ABC", true, false, null,
    ["a", 1], { age: 3 }, { x: 1 },
  ];
  const rootKeys = ["x", "y", "z", "flag", "profile", "newKey"];
  for (let step = 0; step < 300; step++) {
    const current = collectPaths(engine.getData());
    const setPool = [...current, ...rootKeys.map((k) => `/${k}`)];
    const useSet = rand() < 0.7 || current.length === 0;
    const op = useSet
      ? { op: "set" as const, path: pick(setPool, rand), value: pick(values, rand) }
      : { op: "remove" as const, path: pick(current, rand) };
    engine.apply([op]);
    assert.deepEqual(
      engine.getErrors(),
      validate(schema, engine.getData()),
      `diverged after step ${step}: ${JSON.stringify(op)}`,
    );
  }
});

function collectPaths(data: Json): string[] {
  const out: string[] = [];
  const walk = (value: Json, path: string): void => {
    if (Array.isArray(value)) {
      value.forEach((v, i) => {
        out.push(`${path}/${i}`);
        walk(v, `${path}/${i}`);
      });
    } else if (value !== null && typeof value === "object") {
      for (const k of Object.keys(value)) {
        out.push(`${path}/${k}`);
        walk(value[k], `${path}/${k}`);
      }
    }
  };
  walk(data, "");
  return out;
}

function pick<T>(arr: T[], rand: () => number): T {
  return arr[Math.floor(rand() * arr.length)];
}

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
