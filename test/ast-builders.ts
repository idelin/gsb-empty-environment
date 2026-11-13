import type {
  AndNode,
  CompareNode,
  ConditionNode,
  LiteralNode,
  LiteralValue,
  MacroRefNode,
  NotNode,
  Operator,
  OrNode,
} from '../src/types.ts';

export const cmp = (field: string, operator: Operator, value: LiteralValue): CompareNode => ({
  kind: 'compare',
  field,
  operator,
  value,
});

export const and = (...operands: ConditionNode[]): AndNode => ({ kind: 'and', operands });

export const or = (...operands: ConditionNode[]): OrNode => ({ kind: 'or', operands });

export const not = (operand: ConditionNode): NotNode => ({ kind: 'not', operand });

export const lit = (value: LiteralValue): LiteralNode => ({ kind: 'literal', value });

export const macro = (name: string): MacroRefNode => ({ kind: 'macro', name });

/** 判断 AST 中是否还残留未展开的宏引用。 */
export function containsMacro(node: ConditionNode): boolean {
  switch (node.kind) {
    case 'macro':
      return true;
    case 'not':
      return containsMacro(node.operand);
    case 'and':
    case 'or':
      return node.operands.some(containsMacro);
    default:
      return false;
  }
}

