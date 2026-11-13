import assert from 'node:assert/strict';
import { test } from 'node:test';
import { checkRuleCondition } from '../index.ts';
import { and, cmp, not, or } from './ast-builders.ts';
import type { FieldAvailabilityMap } from '../src/types.ts';

/**
 * 模拟字段绑定关系：
 * - fd.name 仅在 openat/open 等文件事件下有值，execve 下恒为空；
 * - proc.name 在所有事件下都有值。
 */
const fieldAvailability: FieldAvailabilityMap = {
  'fd.name': ['openat', 'open', 'unlinkat'],
  'proc.name': ['execve', 'openat', 'open', 'unlinkat'],
};

test('fd.name 绑定 execve：该事件类型下报告 never-matches', () => {
  const report = checkRuleCondition({
    condition: cmp('fd.name', '=', '/etc/shadow'),
    eventTypes: ['execve'],
    fieldAvailability,
  });

  assert.equal(report.ok, false);
  assert.equal(report.findings.length, 1);
  assert.equal(report.findings[0].code, 'never-matches');
  assert.equal(report.findings[0].eventType, 'execve');
});

test('同一规则在 openat 下可匹配、在 execve 下永不匹配，分别报告', () => {
  const report = checkRuleCondition({
    condition: cmp('fd.name', '=', '/etc/shadow'),
    eventTypes: ['openat', 'execve'],
    fieldAvailability,
  });

  assert.equal(report.ok, false);
  const codes = report.findings.map((finding) => `${finding.code}:${finding.eventType}`);
  assert.deepEqual(codes, ['never-matches:execve']);
});

test('or 中一个分支字段不可用、另一个恒真：恒真分支使整体在 execve 下恒真', () => {
  // (fd.name = /etc/shadow) or (not (proc.name = "x"))
  // execve 下：左分支恒假；右分支 proc.name 可用 -> unknown，not unknown -> unknown，
  // 因此整体 unknown，不报告。要构造恒真，用 not 包裹一个在 execve 下恒假的比较。
  // not(fd.name = /etc/shadow) 在 execve 下：false 取反 -> true。
  const condition = or(
    cmp('fd.name', '=', '/etc/shadow'),
    not(cmp('fd.name', 'contains', 'shadow')),
  );

  const report = checkRuleCondition({
    condition,
    eventTypes: ['execve'],
    fieldAvailability,
  });

  assert.equal(report.ok, false);
  assert.equal(report.findings.length, 1);
  assert.equal(report.findings[0].code, 'always-true');
  assert.equal(report.findings[0].eventType, 'execve');
});

test('not 直接反转不可用字段比较：execve 下恒真', () => {
  const report = checkRuleCondition({
    condition: not(cmp('fd.name', '=', '/etc/shadow')),
    eventTypes: ['execve'],
    fieldAvailability,
  });

  assert.equal(report.ok, false);
  assert.equal(report.findings[0].code, 'always-true');
  assert.equal(report.findings[0].eventType, 'execve');
});

test('and 组合中字段不可用：整体在 execve 下恒假', () => {
  const report = checkRuleCondition({
    condition: and(
      cmp('proc.name', '=', 'sshd'),
      cmp('fd.name', 'startswith', '/root/'),
    ),
    eventTypes: ['execve'],
    fieldAvailability,
  });

  assert.equal(report.ok, false);
  assert.equal(report.findings[0].code, 'never-matches');
});

test('字段可用且比较依赖运行期值：无发现，ok 为 true', () => {
  const report = checkRuleCondition({
    condition: and(
      cmp('proc.name', '=', 'sshd'),
      cmp('fd.name', 'startswith', '/root/'),
    ),
    eventTypes: ['openat'],
    fieldAvailability,
  });

  assert.equal(report.ok, true);
  assert.deepEqual(report.findings, []);
});

test('映射中未声明的字段按“任何事件可用”处理，不产生误报', () => {
  const report = checkRuleCondition({
    condition: cmp('evt.dir', '=', '>'),
    eventTypes: ['execve'],
    fieldAvailability,
  });

  assert.equal(report.ok, true);
  assert.deepEqual(report.findings, []);
});

