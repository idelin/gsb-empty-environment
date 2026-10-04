import { Json, Schema, ValidationError, ValidationResult } from '../src/schema';
import { validate, formatPath } from '../src/validator';

let failures = 0;
let passed = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log('ok   - ' + name);
  } catch (err) {
    failures++;
    console.error('FAIL - ' + name);
    console.error('       ' + String(err));
  }
}

function assert(cond: boolean, msg: string): void {
  if (!cond) {
    throw new Error(msg);
  }
}

function assertInvalid(result: ValidationResult): asserts result is { valid: false; error: ValidationError } {
  if (result.valid) {
    throw new Error('expected invalid, got valid');
  }
}

test('primitive types and integer', () => {
  assert(validate(3, { type: 'integer' }).valid, '3 is integer');
  assert(validate(3.5, { type: 'number' }).valid, '3.5 is number');
  const r = validate(3.5, { type: 'integer' });
  assertInvalid(r);
  assert(formatPath(r.error.path) === '$', 'error at root');
  assert(validate('x', { type: 'string' }).valid, 'string ok');
  assert(!validate('x', { type: 'boolean' }).valid, 'string is not boolean');
  assert(validate(null, { type: 'null' }).valid, 'null ok');
});

test('required fields', () => {
  const schema: Schema = {
    type: 'object',
    properties: { a: { type: 'integer' } },
    required: ['a', 'b'],
  };
  const r = validate({ a: 1 }, schema);
  assertInvalid(r);
  assert(r.error.path.length === 0, 'missing property reported at object itself');
  assert(r.error.message.indexOf('"b"') >= 0, 'message names missing key, got: ' + r.error.message);
  assert(validate({ a: 1, b: 2 }, schema).valid, 'all required present');
});

test('array length bounds', () => {
  const schema: Schema = { type: 'array', items: { type: 'integer' }, minItems: 2, maxItems: 3 };
  assert(validate([1, 2], schema).valid, 'within bounds');
  const tooShort = validate([1], schema);
  assertInvalid(tooShort);
  assert(tooShort.error.message.indexOf('minItems') >= 0, 'reports minItems');
  const tooLong = validate([1, 2, 3, 4], schema);
  assertInvalid(tooLong);
  assert(tooLong.error.message.indexOf('maxItems') >= 0, 'reports maxItems');
  const badItem = validate([1, 'x'], schema);
  assertInvalid(badItem);
  assert(formatPath(badItem.error.path) === '$[1]', 'item error path, got ' + formatPath(badItem.error.path));
});

test('shallowest failure wins', () => {
  const schema: Schema = {
    type: 'object',
    properties: {
      shallow: { type: 'string' },
      deep: { type: 'object', properties: { inner: { type: 'string' } } },
    },
  };
  const r = validate({ shallow: 1, deep: { inner: 2 } }, schema);
  assertInvalid(r);
  assert(formatPath(r.error.path) === '$.shallow', 'shallow error wins, got ' + formatPath(r.error.path));
});

test('lexicographic tie-break on equal depth', () => {
  const schema: Schema = {
    type: 'object',
    properties: {
      b: { type: 'string' },
      aa: { type: 'string' },
    },
  };
  const r = validate({ b: 1, aa: 2 }, schema);
  assertInvalid(r);
  assert(formatPath(r.error.path) === '$.aa', 'dictionary order beats insertion order, got ' + formatPath(r.error.path));
});

test('empty recursion: pure $ref loop terminates and is vacuously valid', () => {
  const schema: Schema = {
    $ref: '#/$defs/A',
    $defs: { A: { $ref: '#/$defs/A' } },
  };
  const r = validate({ anything: [1, 2, 3] }, schema);
  assert(r.valid, 'pure recursive ref loop accepts any finite instance');
});

test('empty recursion: recursive object schema vs empty instance', () => {
  const schema: Schema = {
    $ref: '#/$defs/Node',
    $defs: {
      Node: {
        type: 'object',
        properties: { next: { $ref: '#/$defs/Node' }, value: { type: 'integer' } },
        required: ['value'],
      },
    },
  };
  const r = validate({ value: 1 }, schema);
  assert(r.valid, 'finite prefix of recursive schema is valid: ' + JSON.stringify(r));
});

test('recursive linked list: deep failure reported at deep path', () => {
  const schema: Schema = {
    $ref: '#/$defs/Node',
    $defs: {
      Node: {
        type: 'object',
        properties: { next: { $ref: '#/$defs/Node' }, value: { type: 'integer' } },
        required: ['value'],
      },
    },
  };
  const good: Json = { value: 1, next: { value: 2, next: { value: 3 } } };
  assert(validate(good, schema).valid, 'finite list valid');
  const bad: Json = { value: 1, next: { value: 2, next: { value: 'oops' } } };
  const r = validate(bad, schema);
  assertInvalid(r);
  assert(
    formatPath(r.error.path) === '$.next.next.value',
    'deep path reported, got ' + formatPath(r.error.path),
  );
});

test('shared references: one $def used by many properties', () => {
  const schema: Schema = {
    type: 'object',
    properties: {
      x: { $ref: '#/$defs/Pos' },
      y: { $ref: '#/$defs/Pos' },
      z: { $ref: '#/$defs/Pos' },
    },
    required: ['x', 'y', 'z'],
    $defs: { Pos: { type: 'integer' } },
  };
  assert(validate({ x: 1, y: 2, z: 3 }, schema).valid, 'all shared refs valid');
  const r = validate({ x: 1, y: 'no', z: 3 }, schema);
  assertInvalid(r);
  assert(formatPath(r.error.path) === '$.y', 'failure at the offending shared-ref property');
});

test('instance depth budget', () => {
  const schema: Schema = {
    $ref: '#/$defs/Tree',
    $defs: {
      Tree: {
        type: 'object',
        properties: { child: { $ref: '#/$defs/Tree' } },
      },
    },
  };
  const instance: Json = { child: { child: { child: {} } } };
  assert(validate(instance, schema, { maxDepth: 3 }).valid, 'within budget');
  const r = validate(instance, schema, { maxDepth: 2 });
  assertInvalid(r);
  assert(r.error.message.indexOf('depth') >= 0, 'mentions depth budget, got: ' + r.error.message);
  assert(
    formatPath(r.error.path) === '$.child.child.child',
    'error at first node over budget, got ' + formatPath(r.error.path),
  );
});

test('depth budget: shallow type error still wins over deep budget error', () => {
  const schema: Schema = {
    type: 'object',
    properties: {
      a: { type: 'string' },
      nest: { type: 'object', properties: { deep: { type: 'object' } } },
    },
  };
  const r = validate({ a: 1, nest: { deep: {} } }, schema, { maxDepth: 1 });
  assertInvalid(r);
  assert(formatPath(r.error.path) === '$.a', 'shallowest failure selected, got ' + formatPath(r.error.path));
});

test('oneOf: exactly one branch must match', () => {
  const schema: Schema = { oneOf: [{ type: 'string' }, { type: 'integer' }] };
  assert(validate('hi', schema).valid, 'string matches one branch');
  assert(validate(4, schema).valid, 'integer matches one branch');
});

test('oneOf: zero matches explains every branch', () => {
  const schema: Schema = { oneOf: [{ type: 'string' }, { type: 'integer' }] };
  const r = validate(true, schema);
  assertInvalid(r);
  assert(r.error.message.indexOf('none') >= 0, 'message says none matched');
  const branches = r.error.branches;
  assert(branches !== undefined && branches.length === 2, 'both branch failures explained');
  if (branches !== undefined) {
    assert(branches[0].branch === 0 && branches[0].error.message.indexOf('string') >= 0, 'branch 0 explained');
    assert(branches[1].branch === 1 && branches[1].error.message.indexOf('integer') >= 0, 'branch 1 explained');
  }
});

test('oneOf: multiple matches are rejected as non-exclusive', () => {
  const schema: Schema = { oneOf: [{ type: 'number' }, { type: 'integer' }] };
  const r = validate(3, schema);
  assertInvalid(r);
  assert(r.error.message.indexOf('exactly 1') >= 0, 'message explains exclusivity, got: ' + r.error.message);
});

test('recursive JSON document schema with oneOf', () => {
  const schema: Schema = {
    $ref: '#/$defs/Value',
    $defs: {
      Value: {
        oneOf: [
          { type: 'null' },
          { type: 'boolean' },
          { type: 'number' },
          { type: 'string' },
          { type: 'array', items: { $ref: '#/$defs/Value' } },
          {
            type: 'object',
            properties: {},
          },
        ],
      },
    },
  };
  const doc: Json = [1, 'two', null, [true, [3.5]], 'x'];
  assert(validate(doc, schema).valid, 'arbitrary JSON array document valid');
  const bad: Json = [1, [undefined as unknown as Json]];
  const r = validate(bad, schema);
  assertInvalid(r);
  assert(formatPath(r.error.path) === '$', 'oneOf failure reported at its own node, got ' + formatPath(r.error.path));
  const branches = r.error.branches;
  assert(branches !== undefined && branches.length === 6, 'all six branches explained');
  if (branches !== undefined) {
    const arrayBranch = branches[4].error;
    assert(formatPath(arrayBranch.path) === '$[1]', 'array branch points at nested element');
    const inner = arrayBranch.branches;
    assert(inner !== undefined, 'nested oneOf explained recursively');
    if (inner !== undefined) {
      assert(
        formatPath(inner[4].error.path) === '$[1][0]',
        'explanation chain reaches deepest failure, got ' + formatPath(inner[4].error.path),
      );
    }
  }
});

test('unresolvable $ref is a validation failure, not a crash', () => {
  const r = validate(1, { $ref: '#/$defs/Missing' });
  assertInvalid(r);
  assert(r.error.message.indexOf('unresolvable') >= 0, 'reports unresolvable ref');
});

console.log('');
console.log(passed + ' passed, ' + failures + ' failed');
if (failures > 0) {
  process.exit(1);
}
