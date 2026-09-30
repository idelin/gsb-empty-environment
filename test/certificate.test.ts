import { test } from 'node:test';
import assert from 'node:assert/strict';
import { issueCertificate, replayCertificate } from '../src/certificate.ts';
import type { Step } from '../src/plan.ts';
import { required, tNumber, tObject, tString, type Type } from '../src/types.ts';

const input: Type = tObject({ x: required(tNumber), name: required(tString) });

const plan: Step[] = [
  { id: 's1', op: 'compute', target: 'z', expr: { op: 'add', left: { op: 'field', path: '$.x' }, right: { op: 'lit', value: 1 } } },
  { id: 's2', op: 'rename', from: 'name', to: 'label' },
  { id: 's3', op: 'delete', field: 'x' },
];

test('证书可重放：重新推导与证书完全一致', () => {
  const { certificate } = issueCertificate(plan, input);
  const r = replayCertificate(plan, input, certificate);
  assert.ok(r.ok, JSON.stringify(r.mismatches));
});

test('证书可序列化为纯 JSON（对象键无序语义）', () => {
  const { certificate } = issueCertificate(plan, input);
  const round = JSON.parse(JSON.stringify(certificate));
  const r = replayCertificate(plan, input, round);
  assert.ok(r.ok, JSON.stringify(r.mismatches));
});

test('篡改证书：重放检出并定位到具体步骤', () => {
  const { certificate } = issueCertificate(plan, input);
  const tampered = JSON.parse(JSON.stringify(certificate));
  tampered.steps[0].output = { kind: 'object', fields: {} };
  const r = replayCertificate(plan, input, tampered);
  assert.equal(r.ok, false);
  assert.ok(r.mismatches.some((m) => m.startsWith('steps[0]')));
});

test('计划变更：重放报告 planHash 不匹配', () => {
  const { certificate } = issueCertificate(plan, input);
  const changed: Step[] = [...plan.slice(0, 2)];
  const r = replayCertificate(changed, input, certificate);
  assert.equal(r.ok, false);
  assert.ok(r.mismatches.includes('planHash'));
});
