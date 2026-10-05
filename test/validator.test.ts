import { SchemaDocument, ValidationError } from "../src/schema";
import { explainError, formatPath, validate } from "../src/validator";

let failures = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    console.log(`ok - ${name}`);
  } catch (err) {
    failures++;
    console.error(`FAIL - ${name}`);
    console.error(err);
  }
}

function assert(cond: boolean, message: string): void {
  if (!cond) {
    throw new Error(`assertion failed: ${message}`);
  }
}

function assertInvalid(
  doc: SchemaDocument,
  instance: unknown,
  expectedPath: string,
  options?: { maxDepth?: number }
): ValidationError {
  const result = validate(instance as never, doc, options);
  assert(!result.valid, `expected invalid, got valid`);
  if (result.valid) throw new Error("unreachable");
  const actual = formatPath(result.error.path);
  assert(
    actual === expectedPath,
    `expected error at ${expectedPath}, got ${actual} (${result.error.message})`
  );
  return result.error;
}

function assertValid(
  doc: SchemaDocument,
  instance: unknown,
  options?: { maxDepth?: number }
): void {
  const result = validate(instance as never, doc, options);
  assert(
    result.valid,
    `expected valid, got error: ${
      result.valid ? "" : explainError(result.error)
    }`
  );
}

// ---- type checks ----

test("primitive type mismatch reports root path", () => {
  const err = assertInvalid({ type: "number" }, "nope", "$");
  assert(
    err.message.includes("expected number"),
    `unexpected message: ${err.message}`
  );
});

test("null is not an object, arrays are not objects", () => {
  assertInvalid({ type: "object" }, null, "$");
  assertInvalid({ type: "object" }, [1, 2], "$");
  assertInvalid({ type: "array" }, { 0: 1 }, "$");
});

// ---- required fields & tie-breaking ----

test("missing required fields: lexicographically smallest field wins", () => {
  const doc: SchemaDocument = {
    type: "object",
    required: ["b", "a", "c"],
  };
  assertInvalid(doc, {}, "$.a");
});

test("shallow missing field beats deeper nested type error", () => {
  const doc: SchemaDocument = {
    type: "object",
    required: ["z"],
    properties: {
      a: {
        type: "object",
        properties: { b: { type: "object", properties: { c: { type: "number" } } } },
      },
    },
  };
  // Deep error at $.a.b.c would be length 3; missing "z" is length 1.
  assertInvalid(doc, { a: { b: { c: "deep" } } }, "$.z");
});

test("sibling property errors tie-break by field name", () => {
  const doc: SchemaDocument = {
    type: "object",
    properties: { b: { type: "number" }, a: { type: "number" } },
  };
  assertInvalid(doc, { b: "x", a: "y" }, "$.a");
});

test("array item errors tie-break by index", () => {
  const doc: SchemaDocument = {
    type: "array",
    items: { type: "number" },
  };
  assertInvalid(doc, [1, "x", "y"], "$[1]");
});

// ---- array length ----

test("array length bounds", () => {
  const doc: SchemaDocument = { type: "array", minItems: 2, maxItems: 3 };
  assertValid(doc, [1, 2]);
  assertInvalid(doc, [1], "$");
  assertInvalid(doc, [1, 2, 3, 4], "$");
});

test("nested array length error reports the array's path", () => {
  const doc: SchemaDocument = {
    type: "object",
    properties: { tags: { type: "array", minItems: 1 } },
  };
  assertInvalid(doc, { tags: [] }, "$.tags");
});

// ---- recursion ----

test("empty recursion terminates and is vacuously valid", () => {
  const selfRef: SchemaDocument = { $ref: "#" };
  assertValid(selfRef, null);
  assertValid(selfRef, { deeply: [{ nested: "value" }] });

  const pingPong: SchemaDocument = {
    $ref: "#/definitions/a",
    definitions: {
      a: { $ref: "#/definitions/b" },
      b: { $ref: "#/definitions/a" },
    },
  };
  assertValid(pingPong, 42);
});

test("recursive linked list schema validates nested instances", () => {
  const list: SchemaDocument = {
    type: "object",
    required: ["value"],
    properties: {
      value: { type: "number" },
      next: { $ref: "#" },
    },
  };
  assertValid(list, { value: 1, next: { value: 2, next: { value: 3 } } });
  assertInvalid(
    list,
    { value: 1, next: { value: 2, next: { value: "bad" } } },
    "$.next.next.value"
  );
  assertInvalid(list, { value: 1, next: {} }, "$.next.value");
});

test("recursive tree via definitions", () => {
  const tree: SchemaDocument = {
    $ref: "#/definitions/node",
    definitions: {
      node: {
        type: "object",
        required: ["children"],
        properties: {
          children: {
            type: "array",
            items: { $ref: "#/definitions/node" },
            maxItems: 2,
          },
        },
      },
    },
  };
  assertValid(tree, { children: [{ children: [] }, { children: [] }] });
  assertInvalid(
    tree,
    { children: [{ children: [] }, { children: [] }, { children: [] }] },
    "$.children"
  );
  assertInvalid(tree, { children: [{}] }, "$.children[0].children");
});

// ---- shared references ----

test("shared $ref definition validates each use site independently", () => {
  const doc: SchemaDocument = {
    type: "object",
    properties: {
      x: { $ref: "#/definitions/num" },
      y: { $ref: "#/definitions/num" },
    },
    definitions: { num: { type: "number" } },
  };
  assertValid(doc, { x: 1, y: 2 });
  assertInvalid(doc, { x: 1, y: "bad" }, "$.y");
  assertInvalid(doc, { x: "bad", y: 2 }, "$.x");
});

test("shared recursive definition reused across array items", () => {
  const doc: SchemaDocument = {
    type: "array",
    items: { $ref: "#/definitions/chain" },
    definitions: {
      chain: {
        type: "object",
        properties: { next: { $ref: "#/definitions/chain" } },
      },
    },
  };
  assertValid(doc, [{ next: { next: {} } }, {}, { next: {} }]);
  assertInvalid(doc, [{}, { next: 5 }], "$[1].next");
});

// ---- oneOf: mutually exclusive branches ----

test("oneOf accepts exactly one matching branch", () => {
  const doc: SchemaDocument = { oneOf: [{ type: "string" }, { type: "number" }] };
  assertValid(doc, "hello");
  assertValid(doc, 3.14);
});

test("oneOf explains every failing branch", () => {
  const doc: SchemaDocument = {
    type: "object",
    properties: {
      payload: {
        oneOf: [
          { type: "object", required: ["a"] },
          { type: "object", required: ["b"] },
        ],
      },
    },
  };
  const err = assertInvalid(doc, { payload: {} }, "$.payload");
  assert(err.branches !== undefined, "expected branch explanations");
  assert(err.branches!.length === 2, "expected two branch failures");
  assert(err.branches![0].branch === 0, "first failure is branch 0");
  assert(
    formatPath(err.branches![0].error.path) === "$.payload.a",
    `branch 0 path: ${formatPath(err.branches![0].error.path)}`
  );
  assert(
    formatPath(err.branches![1].error.path) === "$.payload.b",
    `branch 1 path: ${formatPath(err.branches![1].error.path)}`
  );
  const text = explainError(err);
  assert(text.includes("branch 0"), "explanation mentions branch 0");
  assert(text.includes("branch 1"), "explanation mentions branch 1");
});

test("oneOf rejects instances matching multiple branches", () => {
  const doc: SchemaDocument = {
    oneOf: [
      { type: "object", required: ["a"] },
      { type: "object", required: ["b"] },
    ],
  };
  const err = assertInvalid(doc, { a: 1, b: 2 }, "$");
  assert(
    err.message.includes("exactly one"),
    `unexpected message: ${err.message}`
  );
});

test("oneOf inside a recursive schema", () => {
  const doc: SchemaDocument = {
    $ref: "#/definitions/expr",
    definitions: {
      expr: {
        oneOf: [
          { type: "number" },
          {
            type: "object",
            required: ["lhs", "rhs"],
            properties: {
              lhs: { $ref: "#/definitions/expr" },
              rhs: { $ref: "#/definitions/expr" },
            },
          },
        ],
      },
    },
  };
  assertValid(doc, { lhs: 1, rhs: { lhs: 2, rhs: 3 } });
  // The root oneOf fails at $; branch 1 explains the nested failure.
  const err = assertInvalid(doc, { lhs: 1, rhs: "nope" }, "$");
  assert(err.branches !== undefined, "expected branch explanations");
  assert(
    formatPath(err.branches![1].error.path) === "$.rhs",
    `branch 1 path: ${formatPath(err.branches![1].error.path)}`
  );
  assert(
    err.branches![1].error.branches !== undefined,
    "nested oneOf failure keeps its own branch explanations"
  );
});

// ---- instance depth budget ----

test("instance depth budget bounds recursion", () => {
  const chain: SchemaDocument = {
    type: "object",
    properties: { child: { $ref: "#" } },
  };
  const instance = { child: { child: { child: {} } } };
  assertValid(chain, instance, { maxDepth: 3 });
  const err = assertInvalid(chain, instance, "$.child.child.child", {
    maxDepth: 2,
  });
  assert(
    err.message.includes("depth budget"),
    `unexpected message: ${err.message}`
  );
});

test("depth budget applies to arrays and leaves scalars alone", () => {
  const doc: SchemaDocument = {
    type: "object",
    properties: {
      list: { type: "array", items: { type: "array", items: { type: "number" } } },
      name: { type: "string" },
    },
  };
  assertValid(doc, { list: [[1]], name: "x" }, { maxDepth: 2 });
  assertValid(doc, { name: "x" }, { maxDepth: 0 });
  assertInvalid(doc, { list: [[1]] }, "$.list[0]", { maxDepth: 1 });
});

test("depth budget rejects negative values", () => {
  let threw = false;
  try {
    validate(null, { type: "null" }, { maxDepth: -1 });
  } catch (e) {
    threw = e instanceof RangeError;
  }
  assert(threw, "expected RangeError for negative maxDepth");
});

// ---- summary ----

if (failures > 0) {
  console.error(`${failures} test(s) failed`);
  throw new Error("test failures");
}
console.log("all tests passed");
