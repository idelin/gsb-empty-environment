import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  DEFAULT_MAX_DEPTH,
  JsonParseError,
  parseJsonTree,
} from '../src/json-parser.ts';
import { assertStructurallyEqual } from './helpers.ts';

describe('parseJsonTree', () => {
  it('parses scalar, object and array literals', () => {
    assert.equal(parseJsonTree('true').root, true);
    assert.equal(parseJsonTree('null').root, null);
    assert.equal(parseJsonTree('  42  ').root, 42);
    assertStructurallyEqual(parseJsonTree('{"a":1,"b":[true,false]}').root, {
      a: 1,
      b: [true, false],
    });
  });

  it('builds objects with a null prototype', () => {
    const { root } = parseJsonTree('{"__proto__":{"x":1},"nested":{"y":2}}');
    assert.equal(Object.getPrototypeOf(root), null);
    assert.equal(Object.getPrototypeOf((root as any).nested), null);
    assertStructurallyEqual((root as any).__proto__, { x: 1 });
    assert.equal(({} as any).x, undefined);
  });

  it('rejects duplicate keys anywhere in the tree', () => {
    assert.throws(
      () => parseJsonTree('{"a":1,"a":2}'),
      (error: unknown) => error instanceof JsonParseError && /Duplicate JSON key/.test(error.message),
    );
    assert.throws(
      () => parseJsonTree('{"outer":[{"k":1},{"k":2,"k":3}]}'),
      (error: unknown) => error instanceof JsonParseError && /Duplicate JSON key "k"/.test(error.message),
    );
  });

  it('rejects malformed and non-JSON input', () => {
    const invalid = [
      '',
      '   ',
      '{',
      '{"a":}',
      '{"a" 1}',
      '[1,]',
      '{"a":1} trailing',
      "{'a':1}",
      'undefined',
      'NaN',
      'Infinity',
      '01',
      '{a:1}',
      '"unterminated',
      '"bad\\x escape"',
      '"lone surrogate \\uD800"',
    ];
    for (const text of invalid) {
      assert.throws(() => parseJsonTree(text), JsonParseError, `should reject: ${text}`);
    }
  });

  it('decodes escapes and valid Unicode surrogate pairs', () => {
    const { root } = parseJsonTree('"emoji \\uD83D\\uDE00 end"');
    assert.equal(root, 'emoji 😀 end');
    assert.equal(parseJsonTree('"\\n\\t\\b\\f\\r\\/\\\\\\""').root, '\n\t\b\f\r/\\"');
  });

  it('enforces the configurable depth ceiling on nested objects', () => {
    const maxDepth = 4;
    const threeLayers = '{"a":{"b":{"c":1}}}';
    const fourLayers = '{"a":{"b":{"c":{"d":1}}}}';
    const fiveLayers = '{"a":{"b":{"c":{"d":{"e":1}}}}}';
    assert.doesNotThrow(() => parseJsonTree(threeLayers, { maxDepth }));
    assert.doesNotThrow(() => parseJsonTree(fourLayers, { maxDepth }));
    assert.throws(
      () => parseJsonTree(fiveLayers, { maxDepth }),
      (error: unknown) => error instanceof JsonParseError && /Maximum nesting depth/.test(error.message),
    );
  });

  it('enforces the depth ceiling on nested arrays', () => {
    assert.throws(
      () => parseJsonTree('[[[[1]]]]', { maxDepth: 3 }),
      (error: unknown) => error instanceof JsonParseError && /Maximum nesting depth/.test(error.message),
    );
  });

  it('defaults to a documented depth ceiling', () => {
    const ok = '{"a":'.repeat(DEFAULT_MAX_DEPTH) + '1' + '}'.repeat(DEFAULT_MAX_DEPTH);
    const tooDeep =
      '{"a":'.repeat(DEFAULT_MAX_DEPTH + 1) + '1' + '}'.repeat(DEFAULT_MAX_DEPTH + 1);
    assert.doesNotThrow(() => parseJsonTree(ok));
    assert.throws(() => parseJsonTree(tooDeep), JsonParseError);
  });

  it('only accepts string input', () => {
    assert.throws(() => parseJsonTree(123 as unknown as string), TypeError);
  });

  it('reports line and column in parse errors', () => {
    try {
      parseJsonTree('{\n  "ok": 1,\n  "bad": ,\n}');
      assert.fail('expected parse failure');
    } catch (error) {
      assert.ok(error instanceof JsonParseError);
      assert.equal(error.line, 3);
      assert.ok(error.column > 1);
    }
  });
});
