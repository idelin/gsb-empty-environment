import assert from 'node:assert/strict';
import { describe, test } from 'node:test';

import {
  assertWasmAuthorized,
  parseWasmModule,
  validateWasmImports,
  WasmParseError,
  type CapabilityTable,
} from '../index.ts';
import { buildWasm } from './wasm-builder.ts';

const capabilityTable: CapabilityTable = {
  allowStart: false,
  modules: {
    env: {
      fields: {
        fetch: { kind: 'func', type: { params: ['i32'], results: ['i32'] } },
        log: { kind: 'func', type: { params: ['i32', 'i32'], results: [] } },
        memory: { kind: 'memory', limits: { min: 1, max: 4, shared: false } },
        table: {
          kind: 'table',
          tableType: { elementType: 'funcref', limits: { min: 2, max: 2, shared: false } },
        },
        counter: { kind: 'global', globalType: { valueType: 'i32', mutable: true } },
      },
    },
  },
};

async function assertWebAssemblyAccepts(bytes: Uint8Array, imports: WebAssembly.Imports) {
  await WebAssembly.instantiate(bytes, imports);
}

describe('parse scope', () => {
  test('only type, import, function and start sections are interpreted', () => {
    const bytes = buildWasm({
      customSection: true,
      types: [
        { params: ['i32'], results: ['i32'] },
        { params: [], results: [] },
      ],
      imports: [
        { module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 },
        { module: 'env', field: 'memory', kind: 'memory', limits: { min: 1, max: 4 } },
      ],
      definedFunctions: [{ typeIndex: 1 }],
      start: 1,
    });

    const parsed = parseWasmModule(bytes);
    assert.equal(parsed.types.length, 2);
    assert.equal(parsed.imports.length, 2);
    assert.deepEqual(parsed.functions, [{ index: 1, typeIndex: 1, imported: false }]);
    assert.deepEqual(parsed.start, { functionIndex: 1 });
  });

  test('rejects malformed magic and truncated binaries', () => {
    assert.throws(
      () => parseWasmModule(Uint8Array.from([0, 0x61, 0x73, 0x6d, 2, 0, 0, 0])),
      WasmParseError,
    );
    const truncated = validateWasmImports(
      Uint8Array.from([0, 0x61, 0x73, 0x6d, 1, 0, 0, 0, 1]),
      capabilityTable,
    );
    assert.equal(truncated.ok, false);
    assert.equal(truncated.issues[0].code, 'parse_error');
  });

  test('rejects sections that appear out of order', () => {
    const startFirst = Uint8Array.from([
      0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
      0x08, 0x01, 0x00,
      0x01, 0x01, 0x00,
    ]);
    const result = validateWasmImports(startFirst, capabilityTable);
    assert.equal(result.ok, false);
    assert.equal(result.issues[0].code, 'parse_error');
    assert.match(result.issues[0].message, /out of order/);
  });

  test('assertWasmAuthorized throws an aggregate error on rejection', () => {
    const bytes = buildWasm({
      types: [{ params: ['i32'], results: ['i32', 'i64'] }],
      imports: [{ module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 }],
    });
    assert.throws(
      () => assertWasmAuthorized(bytes, capabilityTable),
      /authorization failed.*env\.fetch/,
    );
  });
});

describe('multi-value signature attacks', () => {
  test('rejects a same-named import whose multi-value result arity differs', async () => {
    const bytes = buildWasm({
      types: [{ params: ['i32'], results: ['i32', 'i64'] }],
      imports: [{ module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 }],
    });
    await assertWebAssemblyAccepts(bytes, { env: { fetch: () => 0 } });

    const result = validateWasmImports(bytes, capabilityTable);
    assert.equal(result.ok, false);
    assert.equal(result.issues.length, 1);
    assert.equal(result.issues[0].code, 'signature_mismatch');
    assert.deepEqual(result.issues[0].detail, {
      expected: { params: ['i32'], results: ['i32'] },
      actual: { params: ['i32'], results: ['i32', 'i64'] },
    });
  });

  test('rejects a same-named import whose parameter type differs', () => {
    const bytes = buildWasm({
      types: [{ params: ['i64'], results: ['i32'] }],
      imports: [{ module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 }],
    });
    const result = validateWasmImports(bytes, capabilityTable);
    assert.equal(result.issues[0].code, 'signature_mismatch');
    assert.match(result.issues[0].message, /\(i64\) -> \(i32\)/);
  });

  test('accepts an exact multi-value signature when granted', () => {
    const table: CapabilityTable = {
      modules: {
        env: {
          fields: {
            swap: {
              kind: 'func',
              type: { params: ['i32', 'i64'], results: ['i64', 'i32'] },
            },
          },
        },
      },
    };
    const bytes = buildWasm({
      types: [{ params: ['i32', 'i64'], results: ['i64', 'i32'] }],
      imports: [{ module: 'env', field: 'swap', kind: 'func', typeIndex: 0 }],
    });
    assert.equal(validateWasmImports(bytes, table).ok, true);
  });
});

describe('alias and duplicate imports', () => {
  test('rejects an alias import under an ungranted module name', () => {
    const bytes = buildWasm({
      types: [{ params: ['i32'], results: ['i32'] }],
      imports: [{ module: 'attacker-alias', field: 'fetch', kind: 'func', typeIndex: 0 }],
    });
    const result = validateWasmImports(bytes, capabilityTable);
    assert.equal(result.ok, false);
    assert.equal(result.issues[0].code, 'unauthorized_import');
    assert.equal(result.issues[0].module, 'attacker-alias');
  });

  test('rejects duplicate imports of the same module and field', () => {
    const bytes = buildWasm({
      types: [
        { params: ['i32'], results: ['i32'] },
        { params: ['i32', 'i32'], results: [] },
      ],
      imports: [
        { module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 },
        { module: 'env', field: 'log', kind: 'func', typeIndex: 1 },
        { module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 },
      ],
    });
    const result = validateWasmImports(bytes, capabilityTable);
    const duplicate = result.issues.find((issue) => issue.code === 'duplicate_import');
    assert.ok(duplicate);
    assert.equal(duplicate?.field, 'fetch');
  });
});

describe('start section policy', () => {
  test('rejects a valid module whose start function is not authorized', async () => {
    const bytes = buildWasm({
      types: [
        { params: ['i32'], results: ['i32'] },
        { params: [], results: [] },
      ],
      imports: [{ module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 }],
      definedFunctions: [{ typeIndex: 1, body: [0x41, 0x00, 0x10, 0x00, 0x1a, 0x0b] }],
      start: 1,
    });
    await assertWebAssemblyAccepts(bytes, { env: { fetch: () => 0 } });

    const result = validateWasmImports(bytes, capabilityTable);
    assert.equal(result.ok, false);
    assert.equal(result.issues.length, 1);
    assert.equal(result.issues[0].code, 'unauthorized_start');
    assert.equal(result.issues[0].startFunctionIndex, 1);
  });

  test('allows start only when the tier explicitly enables it', () => {
    const bytes = buildWasm({
      types: [
        { params: ['i32'], results: ['i32'] },
        { params: [], results: [] },
      ],
      imports: [{ module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 }],
      definedFunctions: [{ typeIndex: 1, body: [0x0b] }],
      start: 1,
    });
    assert.equal(validateWasmImports(bytes, { ...capabilityTable, allowStart: false }).ok, false);
    assert.equal(validateWasmImports(bytes, { ...capabilityTable, allowStart: true }).ok, true);
  });

  test('rejects an out-of-range or non-void start index even when start is enabled', () => {
    const outOfRange = buildWasm({
      types: [{ params: ['i32'], results: ['i32'] }],
      imports: [{ module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 }],
      start: 9,
    });
    assert.equal(
      validateWasmImports(outOfRange, { ...capabilityTable, allowStart: true }).issues[0].code,
      'invalid_start_function',
    );

    const nonVoid = buildWasm({
      types: [{ params: [], results: ['i32'] }],
      definedFunctions: [{ typeIndex: 0, body: [0x41, 1, 0x0b] }],
      start: 0,
    });
    assert.equal(
      validateWasmImports(nonVoid, { ...capabilityTable, allowStart: true }).issues[0].code,
      'invalid_start_function',
    );
  });
});

describe('memory, table and global imports', () => {
  test('memory imports require an exact granted capability', () => {
    const mismatch = buildWasm({
      imports: [
        { module: 'env', field: 'memory', kind: 'memory', limits: { min: 2, max: 9 } },
      ],
    });
    assert.equal(
      validateWasmImports(mismatch, capabilityTable).issues[0].code,
      'memory_limits_mismatch',
    );

    const exact = buildWasm({
      imports: [
        { module: 'env', field: 'memory', kind: 'memory', limits: { min: 1, max: 4 } },
      ],
    });
    assert.equal(validateWasmImports(exact, capabilityTable).ok, true);
  });

  test('table imports are authorized by element type and limits', () => {
    const mismatch = buildWasm({
      imports: [
        {
          module: 'env',
          field: 'table',
          kind: 'table',
          elementType: 'funcref',
          limits: { min: 3, max: 3 },
        },
      ],
    });
    assert.equal(
      validateWasmImports(mismatch, capabilityTable).issues[0].code,
      'table_type_mismatch',
    );

    const ungranted = buildWasm({
      imports: [
        {
          module: 'env',
          field: 'hidden-table',
          kind: 'table',
          elementType: 'funcref',
          limits: { min: 1, max: null },
        },
      ],
    });
    assert.equal(
      validateWasmImports(ungranted, capabilityTable).issues[0].code,
      'unauthorized_import',
    );
  });

  test('global imports are authorized by value type and mutability', () => {
    const mismatch = buildWasm({
      imports: [{ module: 'env', field: 'counter', kind: 'global', valueType: 'i64' }],
    });
    assert.equal(
      validateWasmImports(mismatch, capabilityTable).issues[0].code,
      'global_type_mismatch',
    );

    const exact = buildWasm({
      imports: [
        { module: 'env', field: 'counter', kind: 'global', valueType: 'i32', mutable: true },
      ],
    });
    assert.equal(validateWasmImports(exact, capabilityTable).ok, true);
  });

  test('rejects an import whose kind conflicts with the granted capability', () => {
    const bytes = buildWasm({
      imports: [
        { module: 'env', field: 'memory', kind: 'global', valueType: 'i32', mutable: true },
      ],
    });
    assert.equal(
      validateWasmImports(bytes, capabilityTable).issues[0].code,
      'import_kind_mismatch',
    );
  });
});

describe('legal module', () => {
  test('accepts a module matching the capability table exactly', async () => {
    const bytes = buildWasm({
      types: [
        { params: ['i32'], results: ['i32'] },
        { params: ['i32', 'i32'], results: [] },
      ],
      imports: [
        { module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 },
        { module: 'env', field: 'log', kind: 'func', typeIndex: 1 },
        { module: 'env', field: 'memory', kind: 'memory', limits: { min: 1, max: 4 } },
        {
          module: 'env',
          field: 'table',
          kind: 'table',
          elementType: 'funcref',
          limits: { min: 2, max: 2 },
        },
        { module: 'env', field: 'counter', kind: 'global', valueType: 'i32', mutable: true },
      ],
    });
    await assertWebAssemblyAccepts(bytes, {
      env: {
        fetch: () => 0,
        log: () => undefined,
        memory: new WebAssembly.Memory({ initial: 1, maximum: 4 }),
        table: new WebAssembly.Table({ initial: 2, maximum: 2, element: 'anyfunc' }),
        counter: new WebAssembly.Global({ value: 'i32', mutable: true }, 0),
      },
    });

    const result = validateWasmImports(bytes, capabilityTable);
    assert.equal(result.ok, true);
    assert.deepEqual(result.issues, []);

    const parsed = assertWasmAuthorized(bytes, capabilityTable);
    assert.equal(parsed.imports.length, 5);
  });

  test('collects every failing import instead of stopping at the first one', () => {
    const bytes = buildWasm({
      types: [{ params: ['i64'], results: ['i32'] }],
      imports: [
        { module: 'env', field: 'fetch', kind: 'func', typeIndex: 0 },
        { module: 'env', field: 'secret', kind: 'global', valueType: 'i32' },
      ],
    });
    const result = validateWasmImports(bytes, capabilityTable);
    assert.equal(result.ok, false);
    assert.deepEqual(
      result.issues.map((issue) => issue.code),
      ['signature_mismatch', 'unauthorized_import'],
    );
  });
});
