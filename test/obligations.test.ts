import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mergeAll, mergeObligations, type Obligation } from '../src/obligations.ts';

const quotaApi10: Obligation = { kind: 'quota', resource: 'api', limit: 10 };
const quotaApi5: Obligation = { kind: 'quota', resource: 'api', limit: 5 };
const quotaDb3: Obligation = { kind: 'quota', resource: 'db', limit: 3 };
const maskSsnEmail: Obligation = { kind: 'field-mask', fields: ['ssn', 'email'] };
const maskDob: Obligation = { kind: 'field-mask', fields: ['dob'] };
const formatJson: Obligation = { kind: 'output-format', format: 'json' };
const formatXml: Obligation = { kind: 'output-format', format: 'xml' };

test('限额取更严（min），字段遮蔽取并集，输出格式相同则去重', () => {
  const merged = mergeObligations([quotaApi10, maskSsnEmail, formatJson], [quotaApi5, quotaDb3, maskDob, formatJson]);
  assert.ok(merged.ok);
  assert.deepEqual(merged.obligations, [
    { kind: 'quota', resource: 'api', limit: 5 },
    { kind: 'quota', resource: 'db', limit: 3 },
    { kind: 'field-mask', fields: ['dob', 'email', 'ssn'] },
    { kind: 'output-format', format: 'json' },
  ]);
});

test('合并满足交换律与结合律（结果与遍历顺序无关）', () => {
  const a = [quotaApi10, maskSsnEmail];
  const b = [quotaApi5, maskDob];
  const c = [quotaDb3, formatJson];

  const ab = mergeObligations(a, b);
  const ba = mergeObligations(b, a);
  assert.ok(ab.ok && ba.ok);
  assert.deepEqual(ab.obligations, ba.obligations);

  const abc = mergeAll([a, b, c]);
  const bca = mergeAll([b, c, a]);
  const cab = mergeAll([c, a, b]);
  assert.ok(abc.ok && bca.ok && cab.ok);
  assert.deepEqual(abc.obligations, bca.obligations);
  assert.deepEqual(bca.obligations, cab.obligations);

  const idem = mergeObligations(a, a);
  const base = mergeObligations([], a);
  assert.ok(idem.ok && base.ok);
  assert.deepEqual(idem.obligations, base.obligations);
});

test('不兼容输出格式产生确定性冲突，与顺序无关', () => {
  const forward = mergeObligations([formatJson], [formatXml]);
  const backward = mergeObligations([formatXml], [formatJson]);
  assert.ok(!forward.ok && !backward.ok);
  assert.deepEqual(forward.conflict, { kind: 'output-format', formats: ['json', 'xml'] });
  assert.deepEqual(forward.conflict, backward.conflict);
});
