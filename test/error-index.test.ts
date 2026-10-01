import { test } from "node:test";
import assert from "node:assert/strict";
import { ErrorIndex } from "../src/error-index.ts";
import { toMultiset, type ValidationError } from "../src/errors.ts";
import { validate, type Schema } from "../src/validator.ts";

function makeError(overrides: Partial<ValidationError> = {}): ValidationError {
  return {
    schemaVersion: "v1",
    rule: "minimum",
    instancePath: ["items", 0, "qty"],
    schemaPath: "#/properties/items/items/properties/qty/minimum",
    message: "must be >= 1",
    params: { minimum: 1 },
    ...overrides,
  };
}

function assertSameErrors(actual: ValidationError[], expected: ValidationError[]): void {
  assert.deepEqual(toMultiset(actual), toMultiset(expected));
}

const itemsSchema: Schema = {
  type: "object",
  properties: {
    items: {
      type: "array",
      items: {
        type: "object",
        properties: { qty: { type: "integer", minimum: 1 } },
        required: ["qty"],
      },
    },
  },
};

test("100 items failing the same rule compress to one range [1,100]", () => {
  const errors: ValidationError[] = [];
  for (let i = 1; i <= 100; i++) {
    errors.push(makeError({ instancePath: ["items", i, "qty"] }));
  }
  const index = ErrorIndex.fromErrors(errors);
  assert.equal(index.totalCount, 100);
  assert.equal(index.groupCount, 1);
  const serialized = index.serialize();
  assert.equal(serialized.groups.length, 1);
  assert.deepEqual(serialized.groups[0].ranges, [[1, 100, 1]]);
  assertSameErrors(index.expand(), errors);
});

test("a missing middle item is NOT reported as a continuous range", () => {
  const errors: ValidationError[] = [];
  for (let i = 1; i <= 100; i++) {
    if (i === 50) continue;
    errors.push(makeError({ instancePath: ["items", i, "qty"] }));
  }
  const index = ErrorIndex.fromErrors(errors);
  const serialized = index.serialize();
  assert.deepEqual(serialized.groups[0].ranges, [
    [1, 49, 1],
    [51, 100, 1],
  ]);
  assert.equal(index.totalCount, 99);
  assertSameErrors(index.expand(), errors);
});

test("errors with different rules are never merged", () => {
  const errors = [
    makeError({ rule: "minimum", message: "must be >= 1", params: { minimum: 1 } }),
    makeError({ rule: "type", message: "must be integer", params: { type: "integer" } }),
  ];
  const index = ErrorIndex.fromErrors(errors);
  assert.equal(index.groupCount, 2);
  assertSameErrors(index.expand(), errors);
});

test("different schema versions stay in separate groups", () => {
  const errors = [
    makeError({ schemaVersion: "v1" }),
    makeError({ schemaVersion: "v2" }),
  ];
  const index = ErrorIndex.fromErrors(errors);
  assert.equal(index.groupCount, 2);
  assert.equal(index.count({ schemaVersion: "v1" }), 1);
  assert.equal(index.count({ schemaVersion: "v2" }), 1);
  assertSameErrors(index.expand({ schemaVersion: "v2" }), [errors[1]]);
});

test("multiple errors at the same path are preserved", () => {
  const errors = [
    makeError({ rule: "minimum" }),
    makeError({ rule: "type", message: "must be integer", params: { type: "integer" } }),
    makeError({ rule: "minimum" }), // exact duplicate of the first
  ];
  const index = ErrorIndex.fromErrors(errors);
  assert.equal(index.totalCount, 3);
  assertSameErrors(index.expand(), errors);
  const serialized = index.serialize();
  const minGroup = serialized.groups.find((g) => g.rule === "minimum");
  assert.deepEqual(minGroup?.ranges, [[0, 0, 2]]);
});

test("online add and retract keep counts conserved without rebuild", () => {
  const index = new ErrorIndex();
  const added: ValidationError[] = [];
  for (let i = 0; i < 100; i++) {
    const err = makeError({ instancePath: ["items", i, "qty"] });
    index.addError(err);
    added.push(err);
  }
  assert.equal(index.totalCount, 100);
  assert.equal(index.groupCount, 1);

  // retract a few, including a range-splitting one
  const retracted = [added[10], added[50], added[99]];
  for (const err of retracted) {
    assert.equal(index.retractError(err), true);
  }
  assert.equal(index.totalCount, 97);
  const remaining = added.filter((e) => !retracted.includes(e));
  assertSameErrors(index.expand(), remaining);

  // retracting again must fail and change nothing
  assert.equal(index.retractError(retracted[0]), false);
  assert.equal(index.totalCount, 97);

  // re-add one back online
  index.addError(retracted[0]);
  assert.equal(index.totalCount, 98);
  assertSameErrors(index.expand(), [...remaining, retracted[0]]);
});

test("expand by rule returns only that rule's errors", () => {
  const errors: ValidationError[] = [];
  for (let i = 0; i < 10; i++) {
    errors.push(makeError({ instancePath: ["items", i, "qty"] }));
    errors.push(
      makeError({
        rule: "required",
        instancePath: ["items", i],
        schemaPath: "#/properties/items/items/required",
        message: "must have required property 'name'",
        params: { missingProperty: "name" },
      }),
    );
  }
  const index = ErrorIndex.fromErrors(errors);
  const minimumOnly = index.expand({ rule: "minimum" });
  assert.equal(minimumOnly.length, 10);
  assert.ok(minimumOnly.every((e) => e.rule === "minimum"));
  assertSameErrors(minimumOnly, errors.filter((e) => e.rule === "minimum"));
  assert.equal(index.count({ rule: "required" }), 10);
});

test("expand by path prefix, including a pinned array index", () => {
  const errors: ValidationError[] = [];
  for (let i = 0; i < 20; i++) {
    errors.push(makeError({ instancePath: ["items", i, "qty"] }));
  }
  errors.push(makeError({ instancePath: ["other", 3, "qty"] }));
  const index = ErrorIndex.fromErrors(errors);

  const underItems = index.expand({ pathPrefix: "/items" });
  assert.equal(underItems.length, 20);

  const pinned = index.expand({ pathPrefix: "/items/7" });
  assert.equal(pinned.length, 1);
  assert.deepEqual(pinned[0].instancePath, ["items", 7, "qty"]);

  const pinnedDeep = index.expand({ pathPrefix: ["items", 7, "qty"] });
  assert.equal(pinnedDeep.length, 1);

  const noMatch = index.expand({ pathPrefix: "/nope" });
  assert.equal(noMatch.length, 0);

  assert.equal(index.count({ pathPrefix: "/items" }), 20);
});

test("original order expansion is stable across serialize/deserialize", () => {
  const errors = [
    makeError({ rule: "required", instancePath: ["a"], schemaPath: "#/required", message: "m1" }),
    makeError({ instancePath: ["items", 2, "qty"] }),
    makeError({ instancePath: ["items", 1, "qty"] }),
  ];
  const index = ErrorIndex.fromErrors(errors);
  const restored = ErrorIndex.deserialize(index.serialize());
  assert.deepEqual(
    restored.expand().map((e) => [e.rule, e.instancePath]),
    index.expand().map((e) => [e.rule, e.instancePath]),
  );
  // group insertion order: required group first, then items group (index ascending inside)
  assert.deepEqual(
    index.expand().map((e) => e.rule),
    ["required", "minimum", "minimum"],
  );
});

test("end-to-end: validator output compresses and expands losslessly", () => {
  const data = {
    items: Array.from({ length: 100 }, () => ({ qty: 0 })),
  };
  const errors = validate(itemsSchema, data, { schemaVersion: "v1" });
  assert.equal(errors.length, 100);
  const index = ErrorIndex.fromErrors(errors);
  assert.equal(index.groupCount, 1);
  assert.equal(index.totalCount, 100);
  assertSameErrors(index.expand(), errors);

  // remove item 50's failure online (e.g. user fixed that row)
  const fixed = errors.find(
    (e) => e.instancePath[0] === "items" && e.instancePath[1] === 50,
  );
  assert.ok(fixed);
  assert.equal(index.retractError(fixed), true);
  assert.equal(index.totalCount, 99);
  const serialized = index.serialize();
  assert.deepEqual(serialized.groups[0].ranges, [
    [0, 49, 1],
    [51, 99, 1],
  ]);
  assertSameErrors(
    index.expand(),
    errors.filter((e) => e !== fixed),
  );
});
