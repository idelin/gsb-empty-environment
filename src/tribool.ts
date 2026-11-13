import type { ConditionNode, FieldAvailabilityMap } from './types.ts';

/**
 * 三值逻辑：
 * - 'false'  在该事件类型下条件恒为假（规则永不匹配）
 * - 'true'   在该事件类型下条件恒为真（规则对每个事件都匹配）
 * - 'unknown' 运行期才知道结果，静态分析无法定论
 *
 * 当字段在某事件类型下恒为空时，引用该字段的比较退化为恒假，
 * 再按 Kleene 强逻辑向上传播：
 *   not unknown = unknown
 *   and：存在 false 即 false；否则存在 unknown 即 unknown；否则 true
 *   or ：存在 true 即 true；否则存在 unknown 即 unknown；否则 false
 */
export type Tribool = 'true' | 'false' | 'unknown';

export function negate(value: Tribool): Tribool {
  if (value === 'true') return 'false';
  if (value === 'false') return 'true';
  return 'unknown';
}

export function andAll(values: Tribool[]): Tribool {
  let sawUnknown = false;
  for (const value of values) {
    if (value === 'false') return 'false';
    if (value === 'unknown') sawUnknown = true;
  }
  return sawUnknown ? 'unknown' : 'true';
}
export function orAll(values: Tribool[]): Tribool {
  let sawUnknown = false;
  for (const value of values) {
    if (value === 'true') return 'true';
    if (value === 'unknown') sawUnknown = true;
  }
  return sawUnknown ? 'unknown' : 'false';
}

/** 字段在该事件类型下是否能取到非空值。映射中缺失的字段视为可用。 */
export function isFieldAvailable(
  field: string,
  eventType: string,
  availability: FieldAvailabilityMap,
): boolean {
  const events = availability[field];
  if (events === undefined) return true;
  return events.includes(eventType);
}

/**
 * 在给定事件类型下对（已展开宏的）条件做三值求值。
 */
export function evaluate(node: ConditionNode, eventType: string, availability: FieldAvailabilityMap): Tribool {
  switch (node.kind) {
    case 'literal':
      // 条件位置的字面量只有布尔 true 为真，其余一律按假处理。
      return node.value === true ? 'true' : 'false';
    case 'compare': {
      if (!isFieldAvailable(node.field, eventType, availability)) {
        return 'false';
      }
      // 字段可用时，比较的真假取决于运行期字段值，静态无法定论。
      return 'unknown';
    }
    case 'not':
      return negate(evaluate(node.operand, eventType, availability));
    case 'and':
      return andAll(node.operands.map((child) => evaluate(child, eventType, availability)));
    case 'or':
      return orAll(node.operands.map((child) => evaluate(child, eventType, availability)));
    case 'macro':
      throw new Error(`未展开的宏引用: ${node.name}（求值前必须先完成宏展开）`);
  }
}
