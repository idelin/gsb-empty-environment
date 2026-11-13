import { checkProcNameTruncation } from './proc-name.ts';
import { evaluate } from './tribool.ts';
import { expandMacros } from './macros.ts';
import type { CheckInput, CheckReport, Finding } from './types.ts';

/**
 * 规则加载前的字段绑定检查。
 *
 * 流程：
 * 1. 展开条件中的宏（循环引用 / 超深 / 未定义会直接抛错，拒绝加载）；
 * 2. 对每个声明的 evt.type 做三值求值：
 *    - 恒假：该事件类型下规则永不匹配（字段在该事件下恒为空导致）；
 *    - 恒真：not/or 组合使规则对该事件类型的每个事件都匹配；
 * 3. 扫描 proc.name 与超长字面量的相等比较。
 */
export function checkRuleCondition(input: CheckInput): CheckReport {
  const macros = input.macros ?? {};
  const expandedCondition = expandMacros(input.condition, macros);

  const findings: Finding[] = [];

  for (const eventType of input.eventTypes) {
    const result = evaluate(expandedCondition, eventType, input.fieldAvailability);
    if (result === 'false') {
      findings.push({
        code: 'never-matches',
        eventType,
        field: null,
        message:
          `条件在事件类型 ${eventType} 下恒为假：引用的字段在该事件下均为空，` +
          `规则对 ${eventType} 事件永不匹配，声明该 evt.type 属于无效绑定`,
      });
    } else if (result === 'true') {
      findings.push({
        code: 'always-true',
        eventType,
        field: null,
        message:
          `条件在事件类型 ${eventType} 下恒为真（not/or 与空字段组合退化），` +
          `规则将匹配每一个 ${eventType} 事件，检测条件实际未生效`,
      });
    }
  }

  findings.push(...checkProcNameTruncation(expandedCondition));

  return {
    ok: findings.length === 0,
    findings,
    expandedCondition,
  };
}

