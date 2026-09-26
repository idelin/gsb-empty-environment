import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate, type Schema } from '../src/index.ts';

const sslSchema: Schema = {
  groups: [
    {
      kind: 'atLeastOne',
      fields: ['cert', 'key'],
      when: { op: 'equals', field: 'ssl', value: true },
    },
  ],
};

test('conditional group applies only when its condition holds', () => {
  assert.equal(validate({ ssl: false }, sslSchema).valid, true, 'condition false -> rule off');
  const on = validate({ ssl: true }, sslSchema);
  assert.equal(on.status, 'missing');
  assert.deepEqual(on.completions, [['cert'], ['key']]);
  assert.equal(validate({ ssl: true, cert: 'c' }, sslSchema).valid, true);
});

test('condition on a false-valued field is decided (false is a value)', () => {
  const schema: Schema = {
    groups: [
      { kind: 'atLeastOne', fields: ['x'], when: { op: 'equals', field: 'flag', value: false } },
    ],
  };
  const result = validate({ flag: false }, schema);
  assert.equal(result.status, 'missing', 'equals(false) must fire on a false-valued field');
});

test('undecided policy "inactive" (default): rule silently off', () => {
  const result = validate({}, sslSchema);
  assert.equal(result.valid, true);
  assert.equal(result.issues.length, 0);
});

test('undecided policy "active": rule enforced', () => {
  const result = validate({}, sslSchema, { onUndecided: 'active' });
  assert.equal(result.status, 'missing');
  assert.deepEqual(result.completions, [['cert'], ['key']]);
});

test('undecided policy "error": issue with instance and rule paths', () => {
  const result = validate({}, sslSchema, { onUndecided: 'error' });
  assert.equal(result.valid, false);
  const issue = result.issues.find((i) => i.type === 'undecided');
  assert.ok(issue);
  assert.equal(issue.instancePath, '');
  assert.equal(issue.rulePath, '/groups/0/when');
  assert.deepEqual(issue.fields, ['ssl']);
});

test('nested conditions: and/or/not compose', () => {
  const schema: Schema = {
    groups: [
      {
        kind: 'exactlyOne',
        fields: ['otp', 'hardwareKey'],
        when: {
          op: 'and',
          all: [
            { op: 'equals', field: 'mfa', value: true },
            { op: 'not', cond: { op: 'present', field: 'trustedDevice' } },
          ],
        },
      },
    ],
  };
  assert.equal(validate({ mfa: true, trustedDevice: 'td' }, schema).valid, true);
  assert.equal(validate({ mfa: true }, schema).status, 'missing');
  assert.equal(validate({ mfa: true, otp: 'x' }, schema).valid, true);
});
