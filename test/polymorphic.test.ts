import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  MAX_POLYMORPHIC_DEPTH,
  PolymorphicDecodeError,
  PolymorphicRegistrationError,
  f,
  parseStrictJson,
  polymorphic,
} from "../index.ts";

type Shape =
  | { kind: "circle"; radius: number }
  | { kind: "square"; side: number }
  | { kind: "labeled"; label: string; note: string | null };

function createShapeBase() {
  const shapes = polymorphic<Shape>();
  shapes.register("circle", {
    fields: { radius: f.number() },
    decode: ({ radius }) => ({ kind: "circle", radius }),
  });
  shapes.register("square", {
    fields: { side: f.number() },
    decode: ({ side }) => ({ kind: "square", side }),
  });
  shapes.register("labeled", {
    fields: {
      label: f.string(),
      note: f.nullable(f.string()),
    },
    decode: ({ label, note }) => ({ kind: "labeled", label, note }),
  });
  return shapes;
}

function decodeCode(input: string): { code: string; path: string } {
  const shapes = createShapeBase();
  try {
    shapes.decode(input);
  } catch (error) {
    assert.ok(error instanceof PolymorphicDecodeError);
    return { code: error.code, path: error.path };
  }
  assert.fail("expected decoding to fail");
}

describe("polymorphic JSON decoding", () => {
  it("registers serial names on a base and decodes a subclass", () => {
    const shapes = createShapeBase();
    assert.deepEqual(shapes.decode('{"type":"circle","radius":2}'), {
      kind: "circle",
      radius: 2,
    });
    assert.deepEqual(shapes.serialNames, ["circle", "square", "labeled"]);
  });

  it("accepts the discriminator anywhere in the object", () => {
    const shapes = createShapeBase();
    assert.deepEqual(shapes.decode('{"radius":3,"type":"circle"}'), {
      kind: "circle",
      radius: 3,
    });
    assert.deepEqual(shapes.decode('{"label":"x","type":"labeled","note":null}'), {
      kind: "labeled",
      label: "x",
      note: null,
    });
  });

  it("rejects unregistered types with no default fallback", () => {
    const shapes = createShapeBase();
    assert.throws(
      () => shapes.decode('{"type":"triangle","base":10,"height":5}'),
      (error: unknown) =>
        error instanceof PolymorphicDecodeError &&
        error.code === "UNKNOWN_TYPE" &&
        error.path === "$.type",
    );
    assert.equal(shapes.serialNames.includes("triangle"), false);
  });

  it("rejects duplicate discriminator keys before overwriting", () => {
    assert.deepEqual(decodeCode('{"type":"circle","radius":1,"type":"square"}'), {
      code: "DUPLICATE_KEY",
      path: "$.type",
    });
    assert.deepEqual(decodeCode('{"radius":1,"type":"circle","type":"circle"}'), {
      code: "DUPLICATE_KEY",
      path: "$.type",
    });
  });

  it("rejects duplicate keys whose spelling differs only by escaping", () => {
    assert.deepEqual(decodeCode('{"type":"circle","radius":1,"\\u0074ype":"square"}'), {
      code: "DUPLICATE_KEY",
      path: "$.type",
    });
  });

  it("rejects duplicate keys anywhere in the document", () => {
    assert.deepEqual(decodeCode('{"type":"circle","radius":1,"extra":{"x":1,"x":2}}'), {
      code: "DUPLICATE_KEY",
      path: "$.extra.x",
    });
  });

  it("rejects missing and non-string discriminators", () => {
    assert.deepEqual(decodeCode('{"radius":1}'), {
      code: "MISSING_TYPE",
      path: "$",
    });
    for (const input of ['{"type":1,"radius":1}', '{"type":null,"radius":1}', '{"type":true,"radius":1}']) {
      assert.deepEqual(decodeCode(input), {
        code: "INVALID_TYPE",
        path: "$.type",
      });
    }
  });

  it("rejects unknown subclass fields and missing required fields", () => {
    assert.deepEqual(decodeCode('{"type":"circle","radius":1,"admin":true}'), {
      code: "UNKNOWN_FIELD",
      path: "$.admin",
    });
    assert.deepEqual(decodeCode('{"type":"circle"}'), {
      code: "MISSING_FIELD",
      path: "$.radius",
    });
  });

  it("rejects unknown fields in nested strict objects", () => {
    const shapes = polymorphic<{ kind: "wrapped"; data: { id: string } }>();
    shapes.register("wrapped", {
      fields: {
        data: f.object({ id: f.string() }),
      },
      decode: ({ data }) => ({ kind: "wrapped", data }),
    });
    assert.throws(
      () => shapes.decode('{"type":"wrapped","data":{"id":"a","secret":"b"}}'),
      (error: unknown) =>
        error instanceof PolymorphicDecodeError &&
        error.code === "UNKNOWN_FIELD" &&
        error.path === "$.data.secret",
    );
  });

  it("rejects a subclass field named type at registration time", () => {
    const shapes = createShapeBase();
    assert.throws(
      () =>
        shapes.register("broken", {
          fields: { type: f.string(), radius: f.number() },
          decode: () => ({ kind: "circle", radius: 1 }),
        }),
      (error: unknown) =>
        error instanceof PolymorphicRegistrationError &&
        error.code === "DISCRIMINATOR_CONFLICT",
    );
    assert.equal(shapes.serialNames.includes("broken"), false);
  });

  it("rejects duplicate serial names", () => {
    const shapes = createShapeBase();
    assert.throws(
      () =>
        shapes.register("circle", {
          fields: { radius: f.number() },
          decode: ({ radius }) => ({ kind: "circle", radius }),
        }),
      (error: unknown) =>
        error instanceof PolymorphicRegistrationError &&
        error.code === "DUPLICATE_SERIAL_NAME",
    );
  });
});

describe("nested polymorphism", () => {
  type Node =
    | { kind: "leaf"; name: string }
    | { kind: "branch"; child: Node };

  const nodes = polymorphic<Node>();
  nodes.register("leaf", {
    fields: { name: f.string() },
    decode: ({ name }) => ({ kind: "leaf", name }),
  });
  nodes.register("branch", {
    fields: { child: nodes },
    decode: ({ child }) => ({ kind: "branch", child }),
  });

  function nestedNode(levels: number): string {
    let json = '{"type":"leaf","name":"bottom"}';
    for (let level = 1; level < levels; level += 1) {
      json = `{"type":"branch","child":${json}}`;
    }
    return json;
  }

  it(`allows exactly ${MAX_POLYMORPHIC_DEPTH} polymorphic levels`, () => {
    assert.doesNotThrow(() => nodes.decode(nestedNode(MAX_POLYMORPHIC_DEPTH)));
  });

  it("rejects one level beyond the depth limit", () => {
    assert.throws(
      () => nodes.decode(nestedNode(MAX_POLYMORPHIC_DEPTH + 1)),
      (error: unknown) =>
        error instanceof PolymorphicDecodeError && error.code === "DEPTH_LIMIT",
    );
  });

  it("counts polymorphic values nested inside arrays", () => {
    const forests = polymorphic<{ kind: "forest"; roots: Node[] }>();
    forests.register("forest", {
      fields: { roots: f.array(nodes) },
      decode: ({ roots }) => ({ kind: "forest", roots }),
    });

    const leaves = '{"type":"leaf","name":"bottom"}';
    assert.doesNotThrow(() =>
      forests.decode(`{"type":"forest","roots":[${leafsAtDepth(MAX_POLYMORPHIC_DEPTH - 1)}]}`),
    );
    assert.throws(
      () => forests.decode(`{"type":"forest","roots":[${leafsAtDepth(MAX_POLYMORPHIC_DEPTH)}]}`),
      (error: unknown) =>
        error instanceof PolymorphicDecodeError && error.code === "DEPTH_LIMIT",
    );

    function leafsAtDepth(polymorphicDepth: number): string {
      let json = leaves;
      for (let level = 1; level < polymorphicDepth; level += 1) {
        json = `{"type":"branch","child":${json}}`;
      }
      return json;
    }
  });
});

describe("strict JSON parser", () => {
  it("parses ordinary JSON values and escapes", () => {
    assert.deepEqual(parseStrictJson('{"s":"a\\"b\\u0041","n":-1.5e2,"a":[true,false,null]}'), {
      s: 'a"bA',
      n: -150,
      a: [true, false, null],
    });
  });

  it("rejects invalid JSON and duplicate keys", () => {
    assert.throws(() => parseStrictJson('{"a":1,}'), {
      name: "PolymorphicDecodeError",
      code: "INVALID_JSON",
    });
    assert.throws(() => parseStrictJson('{"a":1,"a":2}'), {
      name: "PolymorphicDecodeError",
      code: "DUPLICATE_KEY",
    });
  });

  it("does not allow a __proto__ key to mutate the prototype", () => {
    const parsed = parseStrictJson('{"__proto__":{"polluted":true}}') as Record<string, unknown>;
    assert.equal(({} as Record<string, unknown>).polluted, undefined);
    assert.deepEqual(parsed.__proto__, { polluted: true });
  });
});
