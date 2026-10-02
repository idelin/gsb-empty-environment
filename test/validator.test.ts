import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate, type MessageDescriptor } from '../src/index.ts';

const Profile: MessageDescriptor = {
  name: 'Profile',
  fields: [
    { number: 1, name: 'age', type: 'int32', validate: (v) => ((v as number) >= 0 ? undefined : 'age must be >= 0') },
  ],
};

const Account: MessageDescriptor = {
  name: 'Account',
  fields: [
    { number: 1, name: 'id', type: 'uint64', required: true },
    { number: 2, name: 'aliases', type: 'string', repeated: true, required: true },
    { number: 3, name: 'profile', type: 'message', message: Profile, required: true },
  ],
};

test('valid object passes with no issues', () => {
  const r = validate({ id: 1n, aliases: ['a'], profile: { age: 30 } }, Account);
  assert.equal(r.valid, true);
  assert.deepEqual(r.issues, []);
});

test('missing required scalar is reported', () => {
  const r = validate({ aliases: ['a'], profile: { age: 1 } }, Account);
  assert.equal(r.valid, false);
  assert.deepEqual(r.issues.map((i) => i.path), ['id']);
  assert.equal(r.issues[0].rule, 'required');
});

test('empty repeated required field counts as missing', () => {
  const r = validate({ id: 1n, aliases: [], profile: { age: 1 } }, Account);
  assert.equal(r.valid, false);
  assert.equal(r.issues[0].path, 'aliases');
});

test('custom validators run per element of repeated fields', () => {
  const d: MessageDescriptor = {
    name: 'D',
    fields: [{ number: 1, name: 'tags', type: 'string', repeated: true, validate: (v) => ((v as string).length > 0 ? undefined : 'empty tag') }],
  };
  const r = validate({ tags: ['ok', ''] }, d);
  assert.equal(r.valid, false);
  assert.equal(r.issues.length, 1);
  assert.equal(r.issues[0].rule, 'custom');
});

test('nested message issues carry dotted paths', () => {
  const r = validate({ id: 1n, aliases: ['a'], profile: { age: -5 } }, Account);
  assert.equal(r.valid, false);
  assert.equal(r.issues[0].path, 'profile.age');
  assert.equal(r.issues[0].message, 'age must be >= 0');
});

test('missing nested required message is reported at its own path', () => {
  const r = validate({ id: 1n, aliases: ['a'] }, Account);
  assert.equal(r.valid, false);
  assert.equal(r.issues[0].path, 'profile');
});

test('validation depth limit produces an issue instead of runaway recursion', () => {
  const Node: MessageDescriptor = { name: 'Node', fields: [] };
  Node.fields.push({ number: 1, name: 'next', type: 'message', message: Node });
  let obj: Record<string, unknown> = {};
  for (let i = 0; i < 50; i++) obj = { next: obj };
  const r = validate(obj, Node, 8);
  assert.equal(r.valid, false);
  assert.equal(r.issues[0].rule, 'depth');
});
