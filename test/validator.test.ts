import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { StreamArrayValidator, ValidationError, validateArray } from "../src/index.ts";
import type { Json, Schema } from "../src/index.ts";

const orderSchema: Schema = {
  minItems: 2,
  maxItems: 4,
  uniqueKeys: ["id", "customer.email"],
  aggregates: [{ key: "total", sum: { minimum: 10, maximum: 1000 } }],
  items: {
    type: "object",
    required: ["id", "total", "customer"],
    properties: {
      id: { type: "integer", minimum: 1 },
      total: { type: "number", minimum: 0 },
      customer: {
        type: "object",
        required: ["email"],
        properties: {
          email: { type: "string", minLength: 3, pattern: "@" },
          tier: { type: "string", enum: ["gold", "silver"] },
        },
      },
    },
  },
};

const validOrders: Json[] = [
  { id: 1, total: 20, customer: { email: "a@x.io", tier: "gold" } },
  { id: 2, total: 30, customer: { email: "b@x.io", tier: "silver" } },
];

describe("StreamArrayValidator - happy path", () => {
  it("accepts a valid stream and reports sums at finish", () => {
    const v = new StreamArrayValidator(orderSchema);
    validOrders.forEach((order) => v.push(order));
    const result = v.finish();
    assert.equal(result.valid, true);
    assert.equal(result.length, 2);
    assert.deepEqual(result.sums, { total: 50 });
    assert.deepEqual(result.issues, []);
    assert.equal(result.state, "ended");
  });

  it("one-shot validateArray helper works", () => {
    const result = validateArray(validOrders, orderSchema);
    assert.equal(result.valid, true);
  });

  it("finish() is idempotent and returns the same result", () => {
    const v = new StreamArrayValidator(orderSchema);
    v.push(validOrders[0]);
    v.push(validOrders[1]);
    const first = v.finish();
    const second = v.finish();
    assert.equal(first, second);
  });
});

describe("StreamArrayValidator - unfinished stream", () => {
  it("never reports valid=true before finish()", () => {
    const v = new StreamArrayValidator(orderSchema);
    assert.deepEqual(v.status(), { state: "open", length: 0, valid: false });
    v.push(validOrders[0]);
    assert.equal(v.status().state, "open");
    assert.equal(v.status().valid, false);
    v.push(validOrders[1]);
    assert.equal(v.status().valid, false, "still open: minItems/aggregates unchecked");
    const result = v.finish();
    assert.equal(result.valid, true);
  });

  it("throws when pushing after finish()", () => {
    const v = new StreamArrayValidator(orderSchema);
    v.finish();
    assert.throws(() => v.push(validOrders[0]), /ended/);
  });
});

describe("StreamArrayValidator - minItems", () => {
  it("is only checked at finish, with instancePath at root and rulePath minItems", () => {
    const v = new StreamArrayValidator(orderSchema);
    const pushed = v.push(validOrders[0]);
    assert.deepEqual(pushed, []);
    const result = v.finish();
    assert.equal(result.valid, false);
    assert.equal(result.issues.length, 1);
    assert.equal(result.issues[0].rule, "minItems");
    assert.equal(result.issues[0].instancePath, "");
    assert.equal(result.issues[0].rulePath, "minItems");
  });
});

describe("StreamArrayValidator - maxItems", () => {
  it("is enforced incrementally per push (5th item)", () => {
    const v = new StreamArrayValidator(orderSchema);
    for (let id = 1; id <= 4; id++) {
      const issues = v.push({ id, total: 10, customer: { email: `u${id}@x.io` } });
      assert.equal(issues.length, 0);
    }
    const issues = v.push({ id: 5, total: 10, customer: { email: "u5@x.io" } });
    assert.equal(issues[0].rule, "maxItems");
    assert.equal(issues[0].instancePath, "/4");
    assert.equal(issues[0].rulePath, "maxItems");
    const result = v.finish();
    assert.equal(result.length, 4, "rejected item is not counted");
  });
});

describe("StreamArrayValidator - uniqueKeys", () => {
  it("detects when the 100,000th item duplicates the first item", () => {
    const schema: Schema = {
      minItems: 100_000,
      uniqueKeys: ["id", "email"],
      items: {
        type: "object",
        required: ["id", "email"],
        properties: {
          id: { type: "integer" },
          email: { type: "string" },
        },
      },
    };
    const v = new StreamArrayValidator(schema);
    for (let id = 1; id < 100_000; id++) {
      v.push({ id, email: `user${id}@x.io` });
    }
    const issues = v.push({ id: 1, email: "user1@x.io" });
    assert.equal(issues.length, 2);
    assert.deepEqual(
      issues.map((issue) => issue.rulePath),
      ["uniqueKeys/id", "uniqueKeys/email"],
    );
    assert.equal(issues[0].instancePath, "/99999/id");
    assert.equal(issues[1].instancePath, "/99999/email");
    const result = v.finish();
    assert.equal(result.valid, false);
    assert.equal(result.length, 99_999, "duplicate item is rejected");
  });

  it("supports dotted nested unique key paths", () => {
    const v = new StreamArrayValidator({
      uniqueKeys: ["customer.email"],
      items: orderSchema.items,
    });
    v.push(validOrders[0]);
    const issues = v.push({ id: 9, total: 1, customer: { email: "a@x.io" } });
    assert.equal(issues[0].rulePath, "uniqueKeys/customer.email");
    assert.equal(issues[0].instancePath, "/1/customer/email");
  });

  it("treats values of different JSON type as distinct", () => {
    const v = new StreamArrayValidator({
      uniqueKeys: ["code"],
      items: { type: "object", properties: { code: {} } },
    });
    assert.equal(v.push({ code: "1" }).length, 0);
    assert.equal(v.push({ code: 1 }).length, 0);
  });
});

describe("StreamArrayValidator - aggregates", () => {
  it("checks sum minimum at finish with both paths", () => {
    const v = new StreamArrayValidator({
      aggregates: [{ key: "total", sum: { minimum: 100 } }],
      items: { type: "object", properties: { total: { type: "number" } } },
    });
    v.push({ total: 40 });
    v.push({ total: 5 });
    assert.equal(v.status().valid, false, "stream still open");
    const result = v.finish();
    assert.equal(result.valid, false);
    assert.equal(result.sums.total, 45);
    assert.equal(result.issues[0].rulePath, "aggregates/total/sum/minimum");
    assert.equal(result.issues[0].instancePath, "");
  });

  it("checks sum maximum at finish", () => {
    const result = validateArray(
      [{ total: 60 }, { total: 50 }],
      { aggregates: [{ key: "total", sum: { maximum: 100 } }] },
    );
    assert.equal(result.valid, false);
    assert.equal(result.issues[0].rulePath, "aggregates/total/sum/maximum");
  });

  it("passes when sum lies inside bounds", () => {
    const result = validateArray(
      [{ total: 50 }],
      { aggregates: [{ key: "total", sum: { minimum: 10, maximum: 100 } }] },
    );
    assert.equal(result.valid, true);
  });

  it("ignores non-numeric values when summing", () => {
    const result = validateArray(
      [{ total: 10 }, { total: "oops" }, {}],
      { aggregates: [{ key: "total", sum: { minimum: 10, maximum: 10 } }] },
    );
    assert.equal(result.valid, true);
    assert.equal(result.sums.total, 10);
  });
});

describe("StreamArrayValidator - item schema subset", () => {
  it("reports type errors with instance index and rule path", () => {
    const v = new StreamArrayValidator({ items: { type: "integer" } });
    const issues = v.push("nope");
    assert.equal(issues[0].instancePath, "/0");
    assert.equal(issues[0].rulePath, "items/type");
  });

  it("reports nested property errors", () => {
    const v = new StreamArrayValidator(orderSchema);
    const issues = v.push({ id: 1, total: -5, customer: { email: "ab", tier: "bronze" } });
    const paths = issues.map((issue) => issue.rulePath).sort();
    assert.deepEqual(paths, [
      "items/properties/customer/properties/email/minLength",
      "items/properties/customer/properties/email/pattern",
      "items/properties/customer/properties/tier/enum",
      "items/properties/total/minimum",
    ]);
    assert.equal(issues[0].instancePath.startsWith("/0/"), true);
  });

  it("reports missing required properties and escapes pointer tokens", () => {
    const v = new StreamArrayValidator({
      items: {
        type: "object",
        required: ["id", "a/b"],
        properties: { id: { type: "integer" } },
      },
    });
    const issues = v.push({});
    const rulePaths = issues.map((issue) => issue.rulePath).sort();
    assert.deepEqual(rulePaths, ["items/required", "items/required"]);
    assert.ok(issues.some((issue) => issue.instancePath === "/0/a~1b"));
  });

  it("validates string length, pattern, numbers, integer type and enums", () => {
    const result = validateArray(
      [
        { s: "ab", n: 9, whole: 1.5, role: "x" },
      ],
      {
        items: {
          type: "object",
          properties: {
            s: { type: "string", minLength: 3, maxLength: 5, pattern: "^z" },
            n: { type: "number", minimum: 10, maximum: 20 },
            whole: { type: "integer" },
            role: { enum: ["a", "b"] },
          },
        },
      },
    );
    const rules = result.issues.map((issue) => issue.rule).sort();
    assert.deepEqual(rules, ["enum", "minLength", "minimum", "pattern", "type"]);
  });

  it("recurses into nested arrays via items", () => {
    const result = validateArray(
      [[{ v: 1 }]],
      {
        items: {
          type: "array",
          items: {
            type: "object",
            properties: { v: { type: "string" } },
          },
        },
      },
    );
    assert.equal(result.valid, false);
    assert.equal(result.issues[0].instancePath, "/0/0/v");
    assert.equal(result.issues[0].rulePath, "items/items/properties/v/type");
  });
});

describe("StreamArrayValidator - error modes", () => {
  it("collect mode gathers errors from many items and keeps processing", () => {
    const v = new StreamArrayValidator(orderSchema);
    assert.equal(v.push({ bad: true }).length > 0, true);
    assert.equal(v.push({ also: "bad" }).length > 0, true);
    const good = v.push(validOrders[0]);
    assert.deepEqual(good, []);
    const result = v.finish();
    assert.equal(result.length, 1);
    assert.equal(result.issues.length >= 2, true);
  });

  it("throw mode stops on first error and does not mutate length", () => {
    const v = new StreamArrayValidator(orderSchema, "throw");
    v.push(validOrders[0]);
    assert.throws(
      () => v.push(null),
      (error: unknown) => {
        assert.ok(error instanceof ValidationError);
        assert.equal(error.issues[0].rule, "type");
        return true;
      },
    );
    assert.equal(v.status().length, 1, "failed push is not counted");
    const result = v.finish();
    assert.equal(result.length, 1);
  });

  it("throw mode also fires for duplicate keys", () => {
    const v = new StreamArrayValidator({ uniqueKeys: ["id"], items: { type: "object" } }, "throw");
    v.push({ id: 1 });
    assert.throws(() => v.push({ id: 1 }), ValidationError);
    assert.equal(v.status().length, 1);
  });
});

describe("StreamArrayValidator - 100k scale and memory", () => {
  it("handles 100k unique items quickly and keeps O(unique keys) memory", () => {
    const v = new StreamArrayValidator({
      uniqueKeys: ["id"],
      aggregates: [{ key: "n", sum: { minimum: 0 } }],
      items: { type: "object", properties: { id: { type: "integer" }, n: { type: "number" } } },
    });
    const start = Date.now();
    for (let id = 1; id <= 100_000; id++) {
      const issues = v.push({ id, n: 1 });
      assert.equal(issues.length, 0);
    }
    const elapsed = Date.now() - start;
    const result = v.finish();
    assert.equal(result.valid, true);
    assert.equal(result.length, 100_000);
    assert.equal(result.sums.n, 100_000);
    assert.ok(elapsed < 5000, `expected under 5s, got ${elapsed}ms`);
  });
});
