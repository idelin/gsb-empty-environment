import assert from 'node:assert/strict';
import { test } from 'node:test';
import { BodyParseError, parseRequestBody } from '../index.ts';

const declarations = {
  ownerId: 'id64',
  targetId: 'id64',
  count: 'safeInt',
  ratio: 'number',
  label: 'string',
} as const;

function expectErrorCode(text: string, table: Record<string, string>, code: string): void {
  assert.throws(
    () => parseRequestBody(text, table),
    (error: unknown) => {
      assert.ok(error instanceof BodyParseError);
      assert.equal(error.code, code);
      return true;
    },
  );
}

function looseDeepEqual(actual: unknown, expected: unknown): void {
  assert.equal(JSON.stringify(actual), JSON.stringify(expected));
}

test('id64 string above 2^53 is preserved exactly instead of being rounded', () => {
  const parsed = parseRequestBody('{"ownerId":"9007199254740993"}', declarations);
  looseDeepEqual(parsed, { ownerId: '9007199254740993' });
  assert.equal(typeof (parsed as Record<string, unknown>).ownerId, 'string');
});

test('authorization sees the exact submitted id, not the rounded neighbor', () => {
  const grantedObjectId = '9007199254740992';
  const parsed = parseRequestBody(
    '{"ownerId":"9007199254740993","targetId":"9007199254740992"}',
    declarations,
  ) as Record<string, string>;
  assert.notEqual(parsed.ownerId, grantedObjectId);
  assert.equal(parsed.targetId, grantedObjectId);
});

test('id64 numeric literal above the safe integer range is rejected, not rounded', () => {
  expectErrorCode('{"ownerId":9007199254740993}', declarations, 'safeint_overflow');
});

test('id64 rejects exponent notation in both literal and string form (1e3)', () => {
  expectErrorCode('{"ownerId":1e3}', declarations, 'id64_format');
  expectErrorCode('{"ownerId":"1e3"}', declarations, 'id64_format');
  expectErrorCode('{"ownerId":100.0}', declarations, 'id64_format');
});

test('id64 accepts safe-range numeric literals and returns decimal strings', () => {
  looseDeepEqual(parseRequestBody('{"ownerId":42}', declarations), { ownerId: '42' });
  looseDeepEqual(parseRequestBody('{"ownerId":-42}', declarations), { ownerId: '-42' });
  looseDeepEqual(parseRequestBody('{"ownerId":0}', declarations), { ownerId: '0' });
});

test('id64 string enforces signed 64-bit range and no leading zeroes', () => {
  looseDeepEqual(parseRequestBody('{"ownerId":"9223372036854775807"}', declarations), {
    ownerId: '9223372036854775807',
  });
  looseDeepEqual(parseRequestBody('{"ownerId":"-9223372036854775808"}', declarations), {
    ownerId: '-9223372036854775808',
  });
  expectErrorCode('{"ownerId":"9223372036854775808"}', declarations, 'id64_overflow');
  expectErrorCode('{"ownerId":"-9223372036854775809"}', declarations, 'id64_overflow');
  expectErrorCode('{"ownerId":"01"}', declarations, 'id64_format');
  expectErrorCode('{"ownerId":"1.0"}', declarations, 'id64_format');
  expectErrorCode('{"ownerId":""}', declarations, 'id64_format');
  expectErrorCode('{"ownerId":" 42"}', declarations, 'id64_format');
});

test('id64 string is canonicalized, including "-0" to "0"', () => {
  looseDeepEqual(parseRequestBody('{"ownerId":"-0"}', declarations), { ownerId: '0' });
  expectErrorCode('{"ownerId":"000"}', declarations, 'id64_format');
});

test('id64 rejects non-string non-integer types', () => {
  expectErrorCode('{"ownerId":true}', declarations, 'type_mismatch');
  expectErrorCode('{"ownerId":null}', declarations, 'type_mismatch');
  expectErrorCode('{"ownerId":["1"]}', declarations, 'type_mismatch');
});

test('safeInt accepts safe integers and rejects overflow, fractions, exponents and strings', () => {
  looseDeepEqual(parseRequestBody('{"count":9007199254740991}', declarations), {
    count: 9007199254740991,
  });
  looseDeepEqual(parseRequestBody('{"count":-9007199254740991}', declarations), {
    count: -9007199254740991,
  });
  expectErrorCode('{"count":9007199254740992}', declarations, 'safeint_overflow');
  expectErrorCode('{"count":-9007199254740992}', declarations, 'safeint_overflow');
  expectErrorCode('{"count":1e3}', declarations, 'type_mismatch');
  expectErrorCode('{"count":1.5}', declarations, 'type_mismatch');
  expectErrorCode('{"count":"42"}', declarations, 'type_mismatch');
});

test('number accepts decimals and exponents but rejects -0 and non-finite values', () => {
  looseDeepEqual(parseRequestBody('{"ratio":1.5}', declarations), { ratio: 1.5 });
  looseDeepEqual(parseRequestBody('{"ratio":1e3}', declarations), { ratio: 1000 });
  looseDeepEqual(parseRequestBody('{"ratio":1E-2}', declarations), { ratio: 0.01 });
  expectErrorCode('{"ratio":-0}', declarations, 'negative_zero');
  expectErrorCode('{"ratio":-0.0}', declarations, 'negative_zero');
  expectErrorCode('{"ratio":1e999}', declarations, 'number_non_finite');
  expectErrorCode('{"ratio":"1.5"}', declarations, 'type_mismatch');
});

test('string fields only accept JSON strings', () => {
  looseDeepEqual(parseRequestBody('{"label":"hello\\nworld"}', declarations), {
    label: 'hello\nworld',
  });
  looseDeepEqual(parseRequestBody('{"label":"\\u0031"}', declarations), { label: '1' });
  expectErrorCode('{"label":42}', declarations, 'type_mismatch');
});

test('undeclared fields with unsafe integer values are rejected', () => {
  expectErrorCode('{"mystery":9007199254740993}', declarations, 'unsafe_undeclared_number');
  expectErrorCode('{"mystery":-9007199254740993}', declarations, 'unsafe_undeclared_number');
  expectErrorCode('{"mystery":1e21}', declarations, 'unsafe_undeclared_number');
  expectErrorCode('{"mystery":1e999}', declarations, 'number_non_finite');
  expectErrorCode('{"mystery":-0}', declarations, 'negative_zero');
});

test('prototype-pollution keys become ordinary own properties', () => {
  const parsed = parseRequestBody(
    '{"__proto__":{"polluted":true},"constructor":{"x":1},"ok":1}',
    declarations,
  ) as Record<string, unknown>;
  assert.equal(({} as Record<string, unknown>).polluted, undefined);
  const protoDescriptor = Object.getOwnPropertyDescriptor(parsed, '__proto__');
  assert.ok(protoDescriptor);
  assert.equal(JSON.stringify(protoDescriptor.value), '{"polluted":true}');
  assert.ok(Object.prototype.hasOwnProperty.call(parsed, 'constructor'));
  assert.equal(parsed.ok, 1);
});

test('special prototype names do not collide with the declaration lookup', () => {
  const withSpecial = { constructor: 'string', toString: 'safeInt' } as const;
  const parsed = parseRequestBody(
    '{"constructor":"ctor","toString":42,"__proto__":"x"}',
    withSpecial,
  ) as Record<string, unknown>;
  assert.equal(parsed.constructor, 'ctor');
  assert.equal(parsed.toString, 42);
  assert.equal(parsed['__proto__'], 'x');
});

test('undeclared safe values pass through untouched', () => {
  const parsed = parseRequestBody(
    '{"mystery":9007199254740991,"f":1.5,"s":"x","b":false,"n":null,"arr":[1,2,3]}',
    declarations,
  );
  looseDeepEqual(parsed, {
    mystery: 9007199254740991,
    f: 1.5,
    s: 'x',
    b: false,
    n: null,
    arr: [1, 2, 3],
  });
});

test('unsafe values nested in arrays or undeclared objects are rejected', () => {
  expectErrorCode('{"items":[9007199254740993]}', declarations, 'unsafe_undeclared_number');
  expectErrorCode(
    '{"meta":{"ownerId":9007199254740993}}',
    declarations,
    'unsafe_undeclared_number',
  );
});

test('duplicate object keys are rejected at parse time', () => {
  expectErrorCode('{"ownerId":"1","ownerId":"2"}', declarations, 'duplicate_key');
  expectErrorCode('{"a":1,"a":2}', declarations, 'duplicate_key');
  expectErrorCode('{"nested":{"a":1,"a":2}}', declarations, 'duplicate_key');
});

test('malformed JSON is rejected', () => {
  expectErrorCode('', declarations, 'syntax');
  expectErrorCode('{"a":1,}', declarations, 'syntax');
  expectErrorCode("{'a':1}", declarations, 'syntax');
  expectErrorCode('{a:1}', declarations, 'syntax');
  expectErrorCode('{"a":1}garbage', declarations, 'syntax');
  expectErrorCode('{"a":.5}', declarations, 'syntax');
  expectErrorCode('{"a":01}', declarations, 'syntax');
  expectErrorCode('{"a":"unterminated}', declarations, 'syntax');
  expectErrorCode('{"a":tru}', declarations, 'syntax');
});

test('root arrays and nested structures are supported', () => {
  looseDeepEqual(
    parseRequestBody('[{"ownerId":"9007199254740993"},{"count":3}]', declarations),
    [{ ownerId: '9007199254740993' }, { count: 3 }],
  );
});

test('surrogate pairs and unicode escapes decode correctly', () => {
  looseDeepEqual(parseRequestBody('{"label":"\\uD83D\\uDE00"}', declarations), {
    label: '😀',
  });
});

test('object key order and whitespace are handled correctly', () => {
  const parsed = parseRequestBody(
    '  { "count" : 2 , "ratio" : 0.5 }  ',
    declarations,
  ) as Record<string, number>;
  looseDeepEqual(Object.keys(parsed), ['count', 'ratio']);
  assert.equal(parsed.count, 2);
  assert.equal(parsed.ratio, 0.5);
});

test('error reports path and offset for debugging', () => {
  try {
    parseRequestBody('{"meta":{"x":9007199254740993}}', declarations);
    assert.fail('expected throw');
  } catch (error) {
    assert.ok(error instanceof BodyParseError);
    assert.equal(error.path, '$.meta.x');
    assert.ok(error.index > 0);
  }
});
