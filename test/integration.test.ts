import assert from 'node:assert/strict';
import { test } from 'node:test';
import { checkRuleCondition, MacroCycleError } from '../index.ts';
import { and, cmp, containsMacro, macro, not, or } from './ast-builders.ts';
import type { ConditionNode, FieldAvailabilityMap } from '../src/types.ts';

const fieldAvailability: FieldAvailabilityMap = {
  'fd.name': ['openat', 'open'],
  'proc.name': ['execve', 'openat', 'open'],
};

test('端到端：宏展开后 fd.name 对 execve 报 never-matches，对 openat 无问题', () => {
  const macros: Record<string, ConditionNode> = {
    sensitive_file: cmp('fd.name', 'contains', 'shadow'),
  };

  const report = checkRuleCondition({
    condition: and(macro('sensitive_file'), cmp('proc.name', '=', 'cat')),
    eventTypes: ['execve', 'openat'],
    fieldAvailability,
    macros,
  });

  assert.equal(containsMacro(report.expandedCondition), false);
  assert.equal(report.ok, false);
  assert.deepEqual(
    report.findings.map((finding) => `${finding.code}:${finding.eventType}`),
    ['never-matches:execve'],
  );
});

test('端到端：or 恒真场景通过宏展开后仍能识别 always-true', () => {
  const macros: Record<string, ConditionNode> = {
    file_guard: cmp('fd.name', '=', '/etc/shadow'),
  };

  const report = checkRuleCondition({
    condition: or(macro('file_guard'), not(macro('file_guard'))),
    eventTypes: ['execve'],
    fieldAvailability,
    macros,
  });

  assert.equal(report.ok, false);
  assert.equal(report.findings[0].code, 'always-true');
});

test('端到端：长 proc.name 比较与 never-matches 在同一份报告中并存', () => {
  const report = checkRuleCondition({
    condition: and(
      cmp('proc.name', '=', 'a'.repeat(18)),
      cmp('fd.name', '=', '/x'),
    ),
    eventTypes: ['execve'],
    fieldAvailability,
  });

  const codes = report.findings.map((finding) => finding.code).sort();
  assert.deepEqual(codes, ['never-matches', 'proc-name-truncation']);
});

test('端到端：宏环导致规则在加载前被拒绝（抛出而非产生报告）', () => {
  const macros: Record<string, ConditionNode> = {};
  macros.alpha = macro('beta');
  macros.beta = macro('alpha');

  assert.throws(
    () =>
      checkRuleCondition({
        condition: macro('alpha'),
        eventTypes: ['execve'],
        fieldAvailability,
        macros,
      }),
    MacroCycleError,
  );
});

test('端到端：健康规则通过全部检查', () => {
  const macros: Record<string, ConditionNode> = {
    sshd: cmp('proc.name', '=', 'sshd'),
  };

  const report = checkRuleCondition({
    condition: and(macro('sshd'), cmp('fd.name', 'startswith', '/etc/')),
    eventTypes: ['openat'],
    fieldAvailability,
    macros,
  });

  assert.equal(report.ok, true);
  assert.deepEqual(report.findings, []);
  assert.equal(containsMacro(report.expandedCondition), false);
});

