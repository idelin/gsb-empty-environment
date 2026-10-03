import { test } from "node:test";
import assert from "node:assert/strict";
import {
  accepts,
  compareContracts,
  enumerateValues,
  makeInside,
  makeOutside,
  type CompareResult,
  type Issue,
  type JsonSchema,
} from "../src/index.ts";

function breaking(oldS: JsonSchema, newS: JsonSchema): Issue[] {
  const r = compareContracts(oldS, newS);
  assert.equal(r.status, "breaking", `expected breaking, got ${JSON.stringify(r)}`);
  assert.ok(r.issues.length > 0);
  for (const iss of r.issues) {
    assert.equal(typeof iss.instancePath, "string");
    assert.equal(typeof iss.rulePath, "string");
    assert.ok(
      accepts(oldS, iss.counterexample),
      `counterexample ${JSON.stringify(iss.counterexample)} must be accepted by the old contract`,
    );
    assert.ok(
      !accepts(newS, iss.counterexample),
      `counterexample ${JSON.stringify(iss.counterexample)} must be rejected by the new contract`,
    );
  }
  return r.issues;
}

function compatible(oldS: JsonSchema, newS: JsonSchema): void {
  const r = compareContracts(oldS, newS);
  assert.equal(r.status, "compatible", `expected compatible, got ${JSON.stringify(r)}`);
}

function unknown(oldS: JsonSchema, newS: JsonSchema): CompareResult {
  const r = compareContracts(oldS, newS);
  assert.equal(r.status, "unknown", `expected unknown, got ${JSON.stringify(r)}`);
  assert.ok("unsupported" in r && r.unsupported.length > 0);
  return r;
}

// ---------------------------------------------------------------- accepts

test("accepts: basic types and keywords", () => {
  assert.equal(accepts({ type: "integer" }, 3), true);
  assert.equal(accepts({ type: "integer" }, 3.5), false);
  assert.equal(accepts({ type: "number" }, 3.5), true);
  assert.equal(accepts({ type: ["string", "null"] }, null), true);
  assert.equal(accepts({ enum: [1, "a", null] }, "a"), true);
  assert.equal(accepts({ enum: [1, "a", null] }, 2), false);
  assert.equal(accepts({ const: { a: [1, 2] } }, { a: [1, 2] }), true);
  assert.equal(accepts({ minimum: 2, exclusiveMaximum: 5 }, 5), false);
  assert.equal(accepts({ minimum: 2, exclusiveMaximum: 5 }, 4.999), true);
  assert.equal(accepts({ type: "string", minLength: 2 }, "ab"), true);
  assert.equal(accepts({ type: "string", maxLength: 1 }, "é"), true); // code points
  assert.equal(accepts({ type: "array", minItems: 1, items: { type: "integer" } }, [1, 2]), true);
  assert.equal(accepts({ type: "array", items: { type: "integer" } }, [1, "x"]), false);
  assert.equal(
    accepts(
      { type: "object", properties: { a: { type: "integer" } }, required: ["a"], additionalProperties: false },
      { a: 1 },
    ),
    true,
  );
  assert.equal(
    accepts({ type: "object", required: ["a"], additionalProperties: false }, { a: 1, b: 2 }),
    false,
  );
  assert.equal(accepts(true, 42), true);
  assert.equal(accepts(false, 42), false);
});

// ---------------------------------------------------------------- enums

test("enum: superset is compatible, subset is breaking with counterexample", () => {
  compatible({ enum: ["a", "b"] }, { enum: ["a", "b", "c"] });
  const issues = breaking({ enum: ["a", "b", "c"] }, { enum: ["a", "b"] });
  assert.deepEqual(issues[0].counterexample, "c");
  assert.equal(issues[0].rulePath, "/enum");
  assert.equal(issues[0].instancePath, "");
});

test("const folded into enum semantics", () => {
  compatible({ const: "x" }, { enum: ["x", "y"] });
  breaking({ const: "y" }, { enum: ["x"] });
  compatible({ enum: [] }, { type: "string" }); // old accepts nothing
});

test("old boolean type vs finite enum", () => {
  const issues = breaking({ type: "boolean" }, { enum: [true] });
  assert.deepEqual(issues[0].counterexample, false);
});

test("integer interval vs finite enum", () => {
  compatible({ type: "integer", minimum: 1, maximum: 3 }, { enum: [1, 2, 3] });
  const issues = breaking({ type: "integer", minimum: 1, maximum: 3 }, { enum: [1, 2] });
  assert.deepEqual(issues[0].counterexample, 3);
});

test("large integer interval vs small enum finds a gap quickly", () => {
  const issues = breaking({ type: "integer", minimum: 0, maximum: 1_000_000 }, { enum: [0, 1, 2] });
  assert.equal(typeof issues[0].counterexample, "number");
});

// ---------------------------------------------------------------- intervals

test("tightening maximum is breaking; loosening is compatible", () => {
  const issues = breaking(
    { type: "integer", minimum: 0, maximum: 100 },
    { type: "integer", minimum: 0, maximum: 50 },
  );
  assert.deepEqual(issues[0].counterexample, 100);
  assert.equal(issues[0].rulePath, "/maximum");
  compatible(
    { type: "integer", minimum: 0, maximum: 50 },
    { type: "integer", minimum: 0, maximum: 100 },
  );
});

test("tightening minimum is breaking", () => {
  const issues = breaking({ type: "integer", minimum: 0 }, { type: "integer", minimum: 10 });
  assert.deepEqual(issues[0].counterexample, 0);
  assert.equal(issues[0].rulePath, "/minimum");
});

test("unbounded old interval vs bounded new yields finite witnesses", () => {
  const i1 = breaking({ type: "integer" }, { type: "integer", minimum: 0 });
  assert.deepEqual(i1[0].counterexample, -1);
  const i2 = breaking({ type: "integer" }, { type: "integer", maximum: 10 });
  assert.deepEqual(i2[0].counterexample, 11);
  for (const iss of [...i1, ...i2]) {
    assert.ok(Number.isFinite(iss.counterexample as number));
  }
});

test("exclusive bounds", () => {
  compatible({ type: "number", exclusiveMaximum: 10 }, { type: "number", maximum: 10 });
  const issues = breaking({ type: "number", maximum: 10 }, { type: "number", exclusiveMaximum: 10 });
  assert.deepEqual(issues[0].counterexample, 10);
  compatible({ type: "integer", maximum: 9 }, { type: "integer", exclusiveMaximum: 10 });
});

test("integer vs number value types", () => {
  compatible({ type: "integer" }, { type: "number" });
  const issues = breaking({ type: "number" }, { type: "integer" });
  assert.deepEqual(issues[0].counterexample, 0.5);
  assert.equal(issues[0].rulePath, "/type");
});

test("non-integer interval tightening", () => {
  const issues = breaking(
    { type: "number", minimum: 0, maximum: 1 },
    { type: "number", minimum: 0, maximum: 0.25 },
  );
  assert.ok(
    issues.some((iss) => {
      const ce = iss.counterexample as number;
      return ce > 0.25 && ce <= 1 && !Number.isInteger(ce);
    }),
    `expected a non-integer counterexample in (0.25, 1], got ${JSON.stringify(issues)}`,
  );
});

test("type list narrowing is breaking", () => {
  const issues = breaking({ type: ["string", "number"] }, { type: "string" });
  assert.equal(issues[0].rulePath, "/type");
});

test("string length intervals", () => {
  const i1 = breaking({ type: "string", minLength: 1 }, { type: "string", minLength: 2 });
  assert.equal(i1[0].rulePath, "/minLength");
  assert.equal((i1[0].counterexample as string).length, 1);
  const i2 = breaking({ type: "string" }, { type: "string", maxLength: 3 });
  assert.equal(i2[0].rulePath, "/maxLength");
  assert.equal((i2[0].counterexample as string).length, 4);
  compatible({ type: "string", minLength: 2, maxLength: 5 }, { type: "string", minLength: 1 });
});

// ---------------------------------------------------------------- arrays

test("array size intervals", () => {
  const i1 = breaking({ type: "array" }, { type: "array", minItems: 2 });
  assert.deepEqual(i1[0].counterexample, []);
  assert.equal(i1[0].rulePath, "/minItems");
  const i2 = breaking({ type: "array" }, { type: "array", maxItems: 1 });
  assert.equal((i2[0].counterexample as unknown[]).length, 2);
  assert.equal(i2[0].rulePath, "/maxItems");
});

test("items tightening reports instance path and rule path", () => {
  const issues = breaking(
    { type: "array", items: { type: "integer", maximum: 10 } },
    { type: "array", items: { type: "integer", maximum: 5 } },
  );
  assert.equal(issues[0].instancePath, "/0");
  assert.equal(issues[0].rulePath, "/items/maximum");
  assert.deepEqual(issues[0].counterexample, [10]);
});

// ---------------------------------------------------------------- objects

test("adding an optional property is compatible when old forbids extras", () => {
  const oldS = {
    type: "object",
    properties: { a: { type: "string" } },
    required: ["a"],
    additionalProperties: false,
  } as const;
  const newS = {
    type: "object",
    properties: { a: { type: "string" }, b: { type: "integer" } },
    required: ["a"],
    additionalProperties: false,
  } as const;
  compatible(oldS, newS);
});

test("adding an optional property is breaking when old allows arbitrary extras", () => {
  const oldS = { type: "object", properties: { a: { type: "string" } }, required: ["a"] };
  const newS = {
    type: "object",
    properties: { a: { type: "string" }, b: { type: "integer" } },
    required: ["a"],
  };
  const issues = breaking(oldS, newS);
  assert.equal(issues[0].instancePath, "/b");
  assert.equal(issues[0].rulePath, "/properties/b/type");
});

test("adding a required property is breaking", () => {
  const issues = breaking(
    { type: "object", properties: { a: { type: "string" } }, additionalProperties: false },
    {
      type: "object",
      properties: { a: { type: "string" } },
      required: ["a"],
      additionalProperties: false,
    },
  );
  assert.equal(issues[0].rulePath, "/required");
  assert.deepEqual(issues[0].counterexample, {});
});

test("forbidding additional properties is breaking", () => {
  const issues = breaking(
    { type: "object", properties: { a: { type: "integer" } } },
    { type: "object", properties: { a: { type: "integer" } }, additionalProperties: false },
  );
  assert.equal(issues[0].rulePath, "/additionalProperties");
});

test("nested object property tightening carries full paths", () => {
  const oldS = {
    type: "object",
    properties: {
      a: {
        type: "object",
        properties: { b: { type: "integer", maximum: 10 } },
        required: ["b"],
        additionalProperties: false,
      },
    },
    required: ["a"],
    additionalProperties: false,
  } as const;
  const newS = {
    type: "object",
    properties: {
      a: {
        type: "object",
        properties: { b: { type: "integer", maximum: 5 } },
        required: ["b"],
        additionalProperties: false,
      },
    },
    required: ["a"],
    additionalProperties: false,
  } as const;
  const issues = breaking(oldS, newS);
  assert.equal(issues[0].instancePath, "/a/b");
  assert.equal(issues[0].rulePath, "/properties/a/properties/b/maximum");
  assert.deepEqual(issues[0].counterexample, { a: { b: 10 } });
});

// ---------------------------------------------------------------- boolean schemas

test("boolean schemas", () => {
  compatible(false, { type: "string" });
  const issues = breaking({ type: "integer" }, false);
  assert.deepEqual(issues[0].counterexample, 0);
  compatible({ type: "integer" }, true);
});

// ---------------------------------------------------------------- unsupported

test("unsupported keyword in old contract => unknown (never compatible)", () => {
  const r = unknown({ type: "string", format: "email" }, { type: "integer" });
  assert.ok(JSON.stringify(r).includes("/format"));
});

test("unsupported keyword in new contract => unknown when supported part passes", () => {
  const r = unknown({ type: "string", minLength: 1 }, { type: "string", pattern: "^a" });
  assert.ok(JSON.stringify(r).includes("/pattern"));
});

test("unsupported keyword in new contract but definite break still reported", () => {
  const r = compareContracts({ type: "string" }, { type: "integer", pattern: "^a" });
  assert.equal(r.status, "breaking");
  assert.ok("unsupported" in r && r.unsupported.length > 0);
});

test("malformed keyword values are unsupported", () => {
  unknown({ type: "string", minLength: -1 }, { type: "string" });
  unknown({ enum: "nope" as unknown as unknown[] }, {});
  unknown({ type: "funky" }, {});
});

// ---------------------------------------------------------------- generators

test("makeInside / makeOutside sanity", () => {
  const s1 = { type: "integer", minimum: 3, maximum: 9 };
  const inside = makeInside(s1);
  assert.ok(inside.ok && accepts(s1, inside.value));
  const outside = makeOutside(s1);
  assert.ok(outside.ok && !accepts(s1, outside.value));
  assert.equal(makeInside({ enum: [] }).ok, false);
  assert.equal(makeOutside(true).ok, false);
});

test("enumerateValues terminates for finite accepted sets", () => {
  assert.deepEqual([...enumerateValues({ type: "boolean" })], [true, false]);
  assert.deepEqual([...enumerateValues({ type: "integer", minimum: 2, maximum: 4 })], [2, 3, 4]);
  assert.deepEqual([...enumerateValues({ enum: [1, 1, 2] })], [1, 2]);
  assert.deepEqual([...enumerateValues(false)], []);
  const arrs = [...enumerateValues({ type: "array", maxItems: 1, items: { enum: ["x"] } })];
  assert.deepEqual(arrs, [[], ["x"]]);
});
