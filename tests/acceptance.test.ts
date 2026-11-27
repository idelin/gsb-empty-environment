import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  JsonPathError,
  matchJsonPath,
  parseJsonPath,
  queryJsonPath,
} from '../src/index.ts';
import type { JsonValue } from '../src/index.ts';

describe('mode declarations', () => {
  it('defaults to lax when no mode keyword is present', () => {
    assert.equal(parseJsonPath('$.a').mode, 'lax');
    assert.equal(parseJsonPath('lax $.a').mode, 'lax');
    assert.equal(parseJsonPath('strict $.a').mode, 'strict');
  });

  it('rejects trailing tokens', () => {
    assert.throws(() => parseJsonPath('$.a b'), JsonPathError);
  });
});

describe('lax auto-unwrapping', () => {
  it('flattens a single-level array before member access', () => {
    const doc = { a: [{ x: 1 }, { x: 2 }] };
    assert.deepEqual(queryJsonPath(doc, 'lax $.a.x'), [1, 2]);
  });

  it('flattens arbitrarily deeply nested arrays layer by layer', () => {
    const doc = {
      items: [
        [{ x: 1 }, [[{ x: 2 }], { x: 3 }]],
        { x: 4 },
        [[[{ x: 5 }]]],
      ],
    };
    assert.deepEqual(queryJsonPath(doc, 'lax $.items.x'), [1, 2, 3, 4, 5]);
  });

  it('silently drops out-of-bounds indices and non-object member access', () => {
    const doc = { a: [1, 2], b: 'scalar' };
    assert.deepEqual(queryJsonPath(doc, 'lax $.a[5]'), []);
    assert.deepEqual(queryJsonPath(doc, 'lax $.b.missing'), []);
    assert.deepEqual(queryJsonPath(doc, 'lax $.missing.deep.path'), []);
  });

  it('treats a non-array scalar as a singleton array for subscripts', () => {
    assert.deepEqual(queryJsonPath({ a: 7 }, 'lax $.a[0]'), [7]);
    assert.deepEqual(queryJsonPath({ a: 7 }, 'lax $.a[1]'), []);
  });
});

describe('strict structural errors', () => {
  it('does not unwrap arrays: member access on an array throws', () => {
    const doc = { a: [{ x: 1 }] };
    assert.throws(
      () => queryJsonPath(doc, 'strict $.a.x'),
      (error: unknown) =>
        error instanceof JsonPathError && error.code === 'JSON_PATH_MEMBER_OF_NON_OBJECT',
    );
  });

  it('throws on missing members, non-array subscripts and out-of-bounds indices', () => {
    assert.throws(
      () => queryJsonPath({ a: 1 }, 'strict $.a[0]'),
      (error: unknown) =>
        error instanceof JsonPathError && error.code === 'JSON_PATH_SUBSCRIPT_OF_NON_ARRAY',
    );
    assert.throws(
      () => queryJsonPath({ a: [1] }, 'strict $.a[3]'),
      (error: unknown) =>
        error instanceof JsonPathError && error.code === 'JSON_PATH_INDEX_OUT_OF_BOUNDS',
    );
    assert.throws(
      () => queryJsonPath({ a: {} }, 'strict $.a.missing'),
      (error: unknown) =>
        error instanceof JsonPathError && error.code === 'JSON_PATH_MEMBER_NOT_FOUND',
    );
  });

  it('surfaces structured errors with stable symbolic codes and positions', () => {
    try {
      queryJsonPath({ a: [1] }, 'strict $.a[3]');
      assert.fail('expected throw');
    } catch (error) {
      assert.ok(error instanceof JsonPathError);
      assert.equal(error.code, 'JSON_PATH_INDEX_OUT_OF_BOUNDS');
      assert.equal(typeof error.sqlstate, 'string');
      assert.equal(typeof error.position, 'number');
    }
  });
});

describe('filter three-valued logic', () => {
  it('maps strict structural errors inside filters to UNKNOWN instead of aborting', () => {
    const doc = { rows: [{ v: 1 }, { v: 'x' }, { v: 3 }] };
    // strict mode + numeric comparison: the string row raises inside the predicate,
    // becomes UNKNOWN, and the evaluation continues over all rows.
    assert.deepEqual(
      queryJsonPath(doc, 'strict $.rows[*] ? (@.v > 0).v'),
      [1, 3],
    );
  });

  it('keeps UNKNOWN under negation: !(UNKNOWN) === UNKNOWN', () => {
    const doc = { rows: [{ v: 1 }, { v: 'x' }] };
    assert.deepEqual(queryJsonPath(doc, 'strict $.rows[*] ? (!(@.v > 0))'), []);
    // Lax version: a type mismatch (boolean compared numerically) stays UNKNOWN
    // under negation, so neither row is selected.
    assert.deepEqual(
      queryJsonPath(
        { rows: [{ ok: true }, { ok: 'nope' }] } as JsonValue,
        'lax $.rows[*] ? (!(@.ok > 0))',
      ),
      [],
    );
  });

  it('implements Kleene AND/OR truth tables', () => {
    const doc = { rows: [{ a: 1, b: 2 }, { a: 'x', b: 2 }, { a: 1, b: 'y' }, { a: 'x', b: 'y' }] };
    // AND: any FALSE -> FALSE; otherwise UNKNOWN if any side is UNKNOWN.
    assert.deepEqual(
      queryJsonPath(doc, 'strict $.rows[*] ? (@.a == 1 && @.b == 2).a'),
      [1],
    );
    // OR: any TRUE -> TRUE even when the other side is UNKNOWN.
    assert.deepEqual(
      queryJsonPath(doc, 'strict $.rows[*] ? (@.a == 1 || @.b == 99).a'),
      [1, 1],
    );
  });

  it('treats empty-sequence comparisons as FALSE, not UNKNOWN', () => {
    const doc: JsonValue = { rows: [{}, { v: 5 }] };
    assert.deepEqual(
      queryJsonPath(doc, 'strict $.rows[*] ? (@.missing == 5)'),
      [],
    );
    // In strict mode the missing member raises inside the predicate -> UNKNOWN;
    // negation keeps it UNKNOWN, so nothing passes.
    assert.deepEqual(
      queryJsonPath(doc, 'strict $.rows[*] ? (!(@.missing == 5))'),
      [],
    );
    // In lax mode the missing member yields an empty sequence, the comparison
    // is defined FALSE, and negation makes it TRUE for both rows.
    assert.deepEqual(
      queryJsonPath(doc, 'lax $.rows[*] ? (!(@.missing == 5))'),
      [{}, { v: 5 }],
    );
    // A genuine type mismatch (both values present) is UNKNOWN and stays
    // UNKNOWN under negation.
    const typed: JsonValue = { rows: [{ v: 'x' }, { v: 5 }] };
    assert.deepEqual(queryJsonPath(typed, 'strict $.rows[*] ? (!(@.v > 0))'), []);
  });
});

describe('existential sequence comparison', () => {
  it('is TRUE when at least one element pair satisfies the predicate', () => {
    const doc = { nums: [1, 2, 3], targets: [7, 2, 9] };
    assert.equal(matchJsonPath(doc, 'lax $.nums ? (@[*] == 2)'), true);
    assert.equal(matchJsonPath(doc, 'lax $.nums ? (@[*] == 99)'), false);
    assert.equal(matchJsonPath(doc, 'lax $.nums[*] ? ($.targets[*] > @)'), true);
  });

  it('compares every pair and falls back to UNKNOWN only when all pairs error', () => {
    const doc = { nums: [1, 2], other: ['a', 'b'] };
    // Numeric ordering against strings is UNKNOWN for every pair.
    assert.equal(matchJsonPath(doc, 'strict $.nums ? ($.other[*] < @[*])'), false);
  });
});

describe('subscripts, slices and last', () => {
  const letters = ['a', 'b', 'c', 'd', 'e'];

  it('supports comma lists, last arithmetic and negative indices', () => {
    assert.deepEqual(queryJsonPath({ letters }, 'lax $.letters[0, 2]'), ['a', 'c']);
    assert.deepEqual(queryJsonPath({ letters }, 'lax $.letters[last]'), ['e']);
    assert.deepEqual(queryJsonPath({ letters }, 'lax $.letters[last - 1]'), ['d']);
    assert.deepEqual(queryJsonPath({ letters }, 'lax $.letters[-1]'), ['e']);
    assert.deepEqual(queryJsonPath({ letters }, 'lax $.letters[1 to last - 2]'), ['b', 'c']);
  });

  it('clamps out-of-range slices instead of raising', () => {
    assert.deepEqual(queryJsonPath({ letters }, 'strict $.letters[2 to 99]'), ['c', 'd', 'e']);
    assert.deepEqual(queryJsonPath({ letters }, 'strict $.letters[99 to 100]'), ['e']);
  });

  it('supports wildcard array subscripts', () => {
    assert.deepEqual(queryJsonPath({ letters }, 'lax $.letters[*]'), letters);
  });
});

describe('wildcards, recursive descent and methods', () => {
  it('expands wildcard members', () => {
    const doc = { o: { a: 1, b: 2, c: 3 } };
    assert.deepEqual(queryJsonPath(doc, 'lax $.o.*'), [1, 2, 3]);
  });

  it('enumerates recursive descendants depth-first, item first', () => {
    const doc = { a: { b: 1 }, c: [2, { d: 3 }] };
    assert.deepEqual(
      queryJsonPath(doc, 'lax $.**'),
      [doc, { b: 1 }, 1, [2, { d: 3 }], 2, { d: 3 }, 3],
    );
    // `**` already enumerates the array elements; the subsequent lax member
    // access re-enters each element, mirroring PostgreSQL's result cardinality.
    assert.deepEqual(queryJsonPath(doc, 'lax $.**.d'), [3, 3]);
  });

  it('implements .type(), .size() and .double()', () => {
    assert.deepEqual(queryJsonPath({ v: 1 }, 'lax $.v.type()'), ['integer']);
    assert.deepEqual(queryJsonPath({ v: 1.5 }, 'lax $.v.type()'), ['number']);
    assert.deepEqual(queryJsonPath({ v: null }, 'lax $.v.type()'), ['null']);
    assert.deepEqual(queryJsonPath({ v: [1, 2, 3] }, 'lax $.v.size()'), [3]);
    assert.deepEqual(queryJsonPath({ v: { x: 1, y: 2 } }, 'lax $.v.size()'), [2]);
    assert.deepEqual(queryJsonPath({ v: 42 }, 'lax $.v.double()'), [42]);
    assert.deepEqual(queryJsonPath({ v: 'nope' }, 'lax $.v.double()'), []);
    assert.throws(
      () => queryJsonPath({ v: 'nope' }, 'strict $.v.double()'),
      (error: unknown) => error instanceof JsonPathError && error.code === 'JSON_PATH_NUMERIC_EXPECTED',
    );
  });
});

describe('filter operators', () => {
  it('evaluates exists(), arithmetic and like_regex', () => {
    const doc: JsonValue = {
      rows: [
        { name: 'alice', age: 30, active: true },
        { name: 'bob' },
        { name: 'carol', age: 25, active: true },
      ],
    };
    assert.deepEqual(
      queryJsonPath(doc, 'lax $.rows[*] ? (exists(@.age)).name'),
      ['alice', 'carol'],
    );
    assert.deepEqual(
      queryJsonPath(doc, 'lax $.rows[*] ? (exists(@.active)).name'),
      ['alice', 'carol'],
    );
    assert.deepEqual(
      queryJsonPath(doc, 'lax $.rows[*] ? (@.age + 1 >= 26).name'),
      ['alice', 'carol'],
    );
    assert.deepEqual(
      queryJsonPath(doc, 'lax $.rows[*] ? (@.age >= 25).name'),
      ['alice', 'carol'],
    );
    assert.deepEqual(
      queryJsonPath(doc, 'lax $.rows[*] ? (@.age * 2 > 55).name'),
      ['alice'],
    );
    assert.deepEqual(
      queryJsonPath(doc, 'lax $.rows[*] ? (@.name like_regex "^a").name'),
      ['alice'],
    );
    assert.deepEqual(
      queryJsonPath(doc, 'lax $.rows[*] ? (@.name like_regex "BOB" flag "i").name'),
      ['bob'],
    );
  });

  it('supports root/current references and quoted keys', () => {
    const doc = { threshold: 10, rows: [{ v: 5 }, { v: 20 }] };
    assert.deepEqual(
      queryJsonPath(doc, 'lax $.rows[*] ? (@.v > $.threshold).v'),
      [20],
    );
    assert.deepEqual(queryJsonPath({ 'weird key': 1 }, 'lax $."weird key"'), [1]);
  });
});
