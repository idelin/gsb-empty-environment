import assert from "node:assert/strict";
import test from "node:test";

import {
  validateWasmImports,
  type CapabilityTable,
  type WasmValueType,
} from "../index.ts";

interface FunctionType {
  params: WasmValueType[];
  results: WasmValueType[];
}

type ImportFixture =
  | {
      kind: "function";
      module: string;
      field: string;
      typeIndex: number;
    }
  | {
      kind: "memory";
      module: string;
      field: string;
      min: number;
      max?: number;
      shared?: boolean;
      memory64?: boolean;
    }
  | {
      kind: "table";
      module: string;
      field: string;
      elementType: "funcref" | "externref";
      min: number;
      max?: number;
      table64?: boolean;
    }
  | {
      kind: "global";
      module: string;
      field: string;
      valueType: WasmValueType;
      mutable: boolean;
    };

const VALUE_TYPE_BYTES: Record<WasmValueType, number> = {
  i32: 0x7f,
  i64: 0x7e,
  f32: 0x7d,
  f64: 0x7c,
  v128: 0x7b,
  funcref: 0x70,
  externref: 0x6f,
};

function concat(...parts: Array<number | Uint8Array>): Uint8Array {
  const output: number[] = [];
  for (const part of parts) {
    if (typeof part === "number") {
      output.push(part);
    } else {
      output.push(...part);
    }
  }
  return Uint8Array.from(output);
}

function u32(value: number): Uint8Array {
  const output: number[] = [];
  do {
    let byte = value & 0x7f;
    value >>>= 7;
    if (value !== 0) {
      byte |= 0x80;
    }
    output.push(byte);
  } while (value !== 0);
  return Uint8Array.from(output);
}

function text(value: string): Uint8Array {
  const encoded = new TextEncoder().encode(value);
  return concat(u32(encoded.length), encoded);
}

function section(id: number, payload: Uint8Array): Uint8Array {
  return concat(id, u32(payload.length), payload);
}

function typeSection(types: FunctionType[]): Uint8Array {
  const encoded: Array<number | Uint8Array> = [u32(types.length)];
  for (const type of types) {
    encoded.push(
      0x60,
      u32(type.params.length),
      ...type.params.map((value) => VALUE_TYPE_BYTES[value]),
      u32(type.results.length),
      ...type.results.map((value) => VALUE_TYPE_BYTES[value]),
    );
  }
  return section(1, concat(...encoded));
}

function limits(
  min: number,
  max: number | undefined,
  extraFlag: number,
): Uint8Array {
  const hasMax = max !== undefined;
  const flags = (hasMax ? 0x01 : 0) | extraFlag;
  return concat(u32(flags), u32(min), hasMax ? u32(max) : new Uint8Array());
}

function importSection(imports: ImportFixture[]): Uint8Array {
  const encoded: Array<number | Uint8Array> = [u32(imports.length)];
  for (const entry of imports) {
    encoded.push(text(entry.module), text(entry.field));
    switch (entry.kind) {
      case "function":
        encoded.push(0x00, u32(entry.typeIndex));
        break;
      case "table":
        encoded.push(
          0x01,
          VALUE_TYPE_BYTES[entry.elementType],
          limits(
            entry.min,
            entry.max,
            entry.table64 === true ? 0x04 : 0,
          ),
        );
        break;
      case "memory":
        encoded.push(
          0x02,
          limits(
            entry.min,
            entry.max,
            (entry.shared === true ? 0x02 : 0) |
              (entry.memory64 === true ? 0x04 : 0),
          ),
        );
        break;
      case "global":
        encoded.push(
          0x03,
          VALUE_TYPE_BYTES[entry.valueType],
          entry.mutable ? 0x01 : 0x00,
        );
        break;
    }
  }
  return section(2, concat(...encoded));
}

function functionSection(typeIndices: number[]): Uint8Array {
  return section(
    3,
    concat(u32(typeIndices.length), ...typeIndices.map((index) => u32(index))),
  );
}

function startSection(functionIndex: number): Uint8Array {
  return section(8, u32(functionIndex));
}

function moduleWith(options: {
  types?: FunctionType[];
  imports?: ImportFixture[];
  functions?: number[];
  start?: number;
}): Uint8Array {
  const parts: Uint8Array[] = [
    Uint8Array.from([0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00]),
  ];
  if (options.types !== undefined) {
    parts.push(typeSection(options.types));
  }
  if (options.imports !== undefined) {
    parts.push(importSection(options.imports));
  }
  if (options.functions !== undefined) {
    parts.push(functionSection(options.functions));
  }
  if (options.start !== undefined) {
    parts.push(startSection(options.start));
  }
  return concat(...parts);
}

function errorCodes(result: ReturnType<typeof validateWasmImports>): string[] {
  return result.errors.map((error) => error.code);
}

test("accepts a legal module whose imports exactly match capability signatures", () => {
  const wasm = moduleWith({
    types: [
      { params: ["i32"], results: [] },
      { params: ["i32", "i64"], results: ["i32", "i64"] },
    ],
    imports: [
      { kind: "function", module: "env", field: "log", typeIndex: 0 },
      { kind: "function", module: "env", field: "pair", typeIndex: 1 },
    ],
  });
  const capabilities: CapabilityTable = [
    { module: "env", field: "log", params: ["i32"], results: [] },
    {
      module: "env",
      field: "pair",
      params: ["i32", "i64"],
      results: ["i32", "i64"],
    },
  ];

  const result = validateWasmImports(wasm, capabilities);

  assert.equal(result.ok, true);
  assert.equal(result.valid, true);
  assert.equal(result.reason, null);
  assert.deepEqual(result.errors, []);
  assert.equal(result.imports.length, 2);
  assert.ok(result.imports.every((entry) => entry.authorized));
});

test("rejects a multi-value result mismatch and includes the full actual signature", () => {
  const wasm = moduleWith({
    types: [{ params: ["i32"], results: ["i32", "i64"] }],
    imports: [
      { kind: "function", module: "env", field: "pair", typeIndex: 0 },
    ],
  });
  const capabilities: CapabilityTable = [
    { module: "env", field: "pair", params: ["i32"], results: ["i32"] },
  ];

  const result = validateWasmImports(wasm, capabilities);

  assert.equal(result.ok, false);
  assert.deepEqual(errorCodes(result), ["IMPORT_SIGNATURE_MISMATCH"]);
  assert.match(result.reason ?? "", /IMPORT_SIGNATURE_MISMATCH/);
  assert.match(result.reason ?? "", /\(i32\) -> \(i32, i64\)/);
  assert.match(result.reason ?? "", /\(i32\) -> \(i32\)/);
});

test("rejects an aliased import instead of matching only the function signature", () => {
  const wasm = moduleWith({
    types: [{ params: ["i32"], results: [] }],
    imports: [
      { kind: "function", module: "env", field: "trace", typeIndex: 0 },
    ],
  });
  const capabilities: CapabilityTable = [
    { module: "env", field: "log", params: ["i32"], results: [] },
  ];

  const result = validateWasmImports(wasm, capabilities);

  assert.equal(result.ok, false);
  assert.deepEqual(errorCodes(result), ["IMPORT_NOT_AUTHORIZED"]);
  assert.match(result.reason ?? "", /"env"\."trace"/);
});

test("rejects an imported start function without explicit start authorization", () => {
  const wasm = moduleWith({
    types: [{ params: [], results: [] }],
    imports: [
      { kind: "function", module: "env", field: "tick", typeIndex: 0 },
    ],
    start: 0,
  });
  const capabilities: CapabilityTable = [
    { module: "env", field: "tick", params: [], results: [] },
  ];

  const result = validateWasmImports(wasm, capabilities);

  assert.equal(result.ok, false);
  assert.deepEqual(errorCodes(result), ["START_NOT_AUTHORIZED"]);
  assert.match(result.reason ?? "", /without explicit allowStart authorization/);
  assert.equal(result.imports[0].authorized, true);
  assert.equal(result.start?.imported, true);
  assert.equal(result.start?.authorized, false);
});

test("allows an imported start only when allowStart is explicitly true", () => {
  const wasm = moduleWith({
    types: [{ params: [], results: [] }],
    imports: [
      { kind: "function", module: "env", field: "tick", typeIndex: 0 },
    ],
    start: 0,
  });
  const capabilities: CapabilityTable = [
    {
      module: "env",
      field: "tick",
      params: [],
      results: [],
      allowStart: true,
    },
  ];

  const result = validateWasmImports(wasm, capabilities);

  assert.equal(result.ok, true);
  assert.equal(result.start?.authorized, true);
});

test("requires explicit memory, table, and global capabilities", () => {
  const wasm = moduleWith({
    imports: [
      { kind: "memory", module: "env", field: "mem", min: 1, max: 2 },
      {
        kind: "table",
        module: "env",
        field: "tab",
        elementType: "funcref",
        min: 1,
        max: 4,
      },
      {
        kind: "global",
        module: "env",
        field: "mode",
        valueType: "i64",
        mutable: false,
      },
    ],
  });

  const denied = validateWasmImports(wasm, []);
  assert.equal(denied.ok, false);
  assert.deepEqual(errorCodes(denied), [
    "IMPORT_NOT_AUTHORIZED",
    "IMPORT_NOT_AUTHORIZED",
    "IMPORT_NOT_AUTHORIZED",
  ]);

  const granted = validateWasmImports(wasm, [
    { kind: "memory", module: "env", field: "mem", min: 1, max: 2 },
    {
      kind: "table",
      module: "env",
      field: "tab",
      elementType: "funcref",
      min: 1,
      max: 4,
    },
    {
      kind: "global",
      module: "env",
      field: "mode",
      valueType: "i64",
      mutable: false,
    },
  ]);
  assert.equal(granted.ok, true);
  assert.ok(granted.imports.every((entry) => entry.authorized));
});

test("rejects a local start function with a non-empty signature", () => {
  const wasm = moduleWith({
    types: [{ params: [], results: ["i32"] }],
    functions: [0],
    start: 0,
  });

  const result = validateWasmImports(wasm, []);

  assert.equal(result.ok, false);
  assert.deepEqual(errorCodes(result), ["START_SIGNATURE_INVALID"]);
});
