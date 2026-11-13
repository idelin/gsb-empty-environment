import assert from 'node:assert/strict';
import { test } from 'node:test';
import { checkProcNameTruncation, checkRuleCondition, MAX_PROC_NAME_BYTES } from '../index.ts';
import { cmp, not, or } from './ast-builders.ts';
import type { FieldAvailabilityMap } from '../src/types.ts';

const fieldAvailability: FieldAvailabilityMap = {
  'proc.name': ['execve', 'openat'],
};

test('proc.name 与 16 字节 ASCII 字面量做 = 比较：报告截断问题', () => {
  const longName = 'a'.repeat(MAX_PROC_NAME_BYTES + 1);
  assert.equal(Buffer.byteLength(longName), 16);

  const findings = checkProcNameTruncation(cmp('proc.name', '=', longName));

  assert.equal(findings.length, 1);
  assert.equal(findings[0].code, 'proc-name-truncation');
  assert.equal(findings[0].eventType, null);
  assert.equal(findings[0].field, 'proc.name');
});

test('proc.name 与恰好 15 字节字面量比较：不报告', () => {
  const maxName = 'a'.repeat(MAX_PROC_NAME_BYTES);
  const findings = checkProcNameTruncation(cmp('proc.name', '=', maxName));
  assert.deepEqual(findings, []);
});

test('按 UTF-8 字节数计长：6 个中文字符（18 字节）报告，5 个（15 字节）不报告', () => {
  const eighteenBytes = '任务进程名字';
  const fifteenBytes = '任务进程名';
  assert.equal(Buffer.byteLength(eighteenBytes), 18);
  assert.equal(Buffer.byteLength(fifteenBytes), 15);

  assert.equal(checkProcNameTruncation(cmp('proc.name', '=', eighteenBytes)).length, 1);
  assert.deepEqual(checkProcNameTruncation(cmp('proc.name', '=', fifteenBytes)), []);
});

test('!= 比较不受截断检查影响（否定比较不会因此失效）', () => {
  const longName = 'a'.repeat(20);
  assert.deepEqual(checkProcNameTruncation(cmp('proc.name', '!=', longName)), []);
});

test('嵌套在 or/not 中的超长比较同样被扫描到', () => {
  const condition = or(
    not(cmp('proc.name', '==', 'a'.repeat(16))),
    cmp('proc.name', '=', 'sshd'),
  );
  const findings = checkProcNameTruncation(condition);
  assert.equal(findings.length, 1);
});

test('完整检查流程同时报告事件绑定问题与进程名截断问题', () => {
  const report = checkRuleCondition({
    condition: cmp('proc.name', '=', 'a'.repeat(16)),
    eventTypes: ['openat'],
    fieldAvailability,
  });

  assert.equal(report.ok, false);
  assert.deepEqual(
    report.findings.map((finding) => finding.code),
    ['proc-name-truncation'],
  );
});
