import assert from "node:assert/strict";
import test from "node:test";

import {
  StrictDecodeError,
  decodeStrict,
  type MessagePackExtension,
  type MessagePackMap,
  type MessagePackValue,
  type StrictDecodeErrorCode,
  type StrictDecodeOptions,
} from "../index.ts";

function bytes(...values: number[]): Uint8Array {
  return new Uint8Array(values);
}

function ascii(value: string): number[] {
  return Array.from(value, (character) => character.charCodeAt(0));
}

function repeated(byte: number, count: number): number[] {
  return Array.from({ length: count }, () => byte);
}

function expectCode(
  input: Uint8Array,
  code: StrictDecodeErrorCode,
  options?: StrictDecodeOptions,
): void {
  assert.throws(
    () => decodeStrict(input, options),
    (error: unknown) =>
      error instanceof StrictDecodeError && error.code === code,
  );
}

function extension(value: MessagePackValue): MessagePackExtension {
  assert.ok(
    typeof value === "object" &&
      value !== null &&
      "type" in value &&
      "data" in value,
  );
  return value as MessagePackExtension;
}

function mapValue(value: MessagePackValue): MessagePackMap {
  assert.ok(
    typeof value === "object" &&
      value !== null &&
      !Array.isArray(value) &&
      !(value instanceof Uint8Array),
  );
  return value as MessagePackMap;
}

test("decodes one canonical MessagePack document", () => {
  const encoded = bytes(
    0x82,
    0xa1,
    0x61,
    0x01,
    0xa1,
    0x62,
    0x92,
    0xc2,
    0xa1,
    0x63,
  );

  assert.deepEqual(decodeStrict(encoded), {
    a: 1,
    b: [false, "c"],
  });
});

test("accepts canonical integer boundary encodings", () => {
  const cases: Array<[number[], number | bigint]> = [
    [[0x00], 0],
    [[0x7f], 127],
    [[0xe0], -32],
    [[0xff], -1],
    [[0xcc, 0x80], 128],
    [[0xcd, 0x01, 0x00], 256],
    [[0xce, 0x00, 0x01, 0x00, 0x00], 65_536],
    [[0xcf, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00], 2 ** 32],
    [[0xd0, 0xdf], -33],
    [[0xd1, 0xff, 0x7f], -129],
    [[0xd2, 0xff, 0xff, 0x7f, 0xff], -32_769],
    [[0xd3, 0xff, 0xff, 0xff, 0xff, 0x7f, 0xff, 0xff, 0xff], -((2 ** 31) + 1)],
  ];

  for (const [input, expected] of cases) {
    assert.equal(decodeStrict(bytes(...input)), expected);
  }
});

test("rejects integers that are not minimally encoded", () => {
  const cases: number[][] = [
    [0xcc, 0x00],
    [0xcc, 0x7f],
    [0xcd, 0x00, 0xff],
    [0xce, 0x00, 0x00, 0xff, 0xff],
    [0xcf, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff],
    [0xd0, 0x00],
    [0xd0, 0xe0],
    [0xd1, 0xff, 0x80],
    [0xd2, 0xff, 0xff, 0x80, 0x00],
    [0xd3, 0xff, 0xff, 0xff, 0xff, 0x80, 0x00, 0x00, 0x00],
    [0xd3, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01],
  ];

  for (const input of cases) {
    expectCode(bytes(...input), "NON_CANONICAL_INTEGER");
  }
});

test("uses bigint only for unsafe 64-bit integer values", () => {
  assert.equal(
    decodeStrict(
      bytes(0xcf, 0x00, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00),
    ),
    9_007_199_254_740_992n,
  );
  assert.equal(
    decodeStrict(
      bytes(0xcf, 0x00, 0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01),
    ),
    9_007_199_254_740_993n,
  );
  assert.equal(
    decodeStrict(
      bytes(0xcf, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff),
    ),
    18_446_744_073_709_551_615n,
  );
  assert.equal(
    decodeStrict(
      bytes(0xd3, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00),
    ),
    -9_223_372_036_854_775_808n,
  );
});

test("rejects verbose string, array, map, and binary encodings", () => {
  expectCode(bytes(0xd9, 0x01, 0x61), "NON_CANONICAL_STRING");
  expectCode(bytes(0xda, 0x00, 0x01, 0x61), "NON_CANONICAL_STRING");
  expectCode(
    bytes(0xdb, 0x00, 0x00, 0x00, 0x01, 0x61),
    "NON_CANONICAL_STRING",
  );

  expectCode(bytes(0xdc, 0x00, 0x01, 0xc0), "NON_CANONICAL_ARRAY");
  expectCode(
    bytes(0xdd, 0x00, 0x00, 0x00, 0x01, 0xc0),
    "NON_CANONICAL_ARRAY",
  );

  expectCode(bytes(0xde, 0x00, 0x01, 0xa1, 0x61, 0xc0), "NON_CANONICAL_MAP");
  expectCode(
    bytes(0xdf, 0x00, 0x00, 0x00, 0x01, 0xa1, 0x61, 0xc0),
    "NON_CANONICAL_MAP",
  );

  expectCode(bytes(0xc5, 0x00, 0x01, 0x00), "NON_CANONICAL_BINARY");
  expectCode(
    bytes(0xc6, 0x00, 0x00, 0x00, 0x01, 0x00),
    "NON_CANONICAL_BINARY",
  );
});

test("accepts the shortest string and array container at each boundary", () => {
  const str32 = bytes(
    0xd9,
    0x20,
    ...repeated(0x61, 32),
  );
  assert.equal(decodeStrict(str32), "a".repeat(32));

  const str256 = bytes(
    0xda,
    0x01,
    0x00,
    ...repeated(0x61, 256),
  );
  assert.equal(decodeStrict(str256), "a".repeat(256));

  const array16 = bytes(0xdc, 0x00, 0x10, ...repeated(0xc0, 16));
  assert.deepEqual(decodeStrict(array16), Array(16).fill(null));
});

test("requires unique string map keys", () => {
  expectCode(
    bytes(0x82, 0xa1, 0x61, 0x01, 0xa1, 0x61, 0x02),
    "DUPLICATE_MAP_KEY",
  );
  expectCode(bytes(0x81, 0x01, 0x02), "INVALID_MAP_KEY");
  expectCode(bytes(0x81, 0x80, 0x01), "INVALID_MAP_KEY");
});

test("keeps prototype-like map keys as own data properties", () => {
  const before = Object.prototype.hasOwnProperty.call(
    Object.prototype,
    "polluted",
  );
  const value = decodeStrict(
    bytes(0x81, 0xa9, ...ascii("__proto__"), 0xa8, ...ascii("polluted")),
  );
  const map = mapValue(value);

  assert.equal(
    Object.getOwnPropertyDescriptor(map, "__proto__")?.value,
    "polluted",
  );
  assert.equal(
    Object.prototype.hasOwnProperty.call(Object.prototype, "polluted"),
    before,
  );
});

test("rejects unregistered extension types", () => {
  expectCode(bytes(0xd4, 0x05, 0x00), "EXTENSION_TYPE_NOT_ALLOWED");
  expectCode(
    bytes(0xd4, 0x05, 0x00),
    "EXTENSION_TYPE_NOT_ALLOWED",
    { allowedExtTypes: [2] },
  );
});

test("passes through only caller-whitelisted extension payloads", () => {
  const result = decodeStrict(bytes(0xd4, 0x02, 0x7f), {
    allowedExtTypes: new Set([-1, 2]),
  });
  const ext = extension(result);

  assert.equal(ext.type, 2);
  assert.deepEqual(ext.data, bytes(0x7f));
});

test("validates timestamp extension payload lengths", () => {
  const options = { allowedExtTypes: [-1] };

  assert.equal(
    extension(decodeStrict(bytes(0xd6, 0xff, 0x00, 0x00, 0x00, 0x00), options))
      .type,
    -1,
  );
  assert.equal(
    extension(
      decodeStrict(
        bytes(0xd7, 0xff, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00),
        options,
      ),
    ).type,
    -1,
  );
  assert.equal(
    extension(
      decodeStrict(
        bytes(0xc7, 0x0c, 0xff, ...repeated(0x00, 12)),
        options,
      ),
    ).type,
    -1,
  );

  for (const length of [1, 2, 3, 5, 6, 7, 9, 10, 11, 13, 16]) {
    const encoder =
      length === 1
        ? bytes(0xd4)
        : length === 2
          ? bytes(0xd5)
          : length === 16
            ? bytes(0xd8)
            : bytes(0xc7, length);
    expectCode(
      bytes(...encoder, 0xff, ...repeated(0x00, length)),
      "INVALID_TIMESTAMP_LENGTH",
      options,
    );
  }
});

test("rejects verbose extension length encodings", () => {
  expectCode(
    bytes(0xc7, 0x04, 0x02, 0x00, 0x00, 0x00, 0x00),
    "NON_CANONICAL_EXTENSION",
    { allowedExtTypes: [2] },
  );
  expectCode(
    bytes(0xc8, 0x00, 0x04, 0x02, 0x00, 0x00, 0x00, 0x00),
    "NON_CANONICAL_EXTENSION",
    { allowedExtTypes: [2] },
  );
});

test("enforces depth and total element limits", () => {
  const nested = bytes(0x91, 0x91, 0x90);
  assert.doesNotThrow(() => decodeStrict(nested, { maxDepth: 3 }));
  expectCode(nested, "MAX_DEPTH_EXCEEDED", { maxDepth: 2 });

  const threeElements = bytes(0x92, 0x01, 0x02);
  assert.doesNotThrow(() => decodeStrict(threeElements, { maxElements: 3 }));
  expectCode(threeElements, "MAX_ELEMENTS_EXCEEDED", { maxElements: 2 });

  const map = bytes(0x81, 0xa1, 0x61, 0x01);
  assert.doesNotThrow(() => decodeStrict(map, { maxElements: 3 }));
  expectCode(map, "MAX_ELEMENTS_EXCEEDED", { maxElements: 2 });
});

test("rejects trailing bytes and truncated input", () => {
  expectCode(bytes(0x01, 0x02), "TRAILING_BYTES");
  expectCode(bytes(0xa2, 0x61), "TRUNCATED");
  expectCode(bytes(0x81, 0xa1), "TRUNCATED");
});

test("rejects invalid UTF-8 string payloads", () => {
  expectCode(bytes(0xa1, 0xff), "INVALID_UTF8");
});

test("validates decoder options before processing input", () => {
  expectCode(bytes(0x01), "INVALID_OPTIONS", {
    allowedExtTypes: [128],
  });
  expectCode(bytes(0x01), "INVALID_OPTIONS", {
    maxDepth: -1,
  });
  expectCode(bytes(0x01), "INVALID_OPTIONS", {
    maxElements: 1.5,
  });
});
