import type { ConditionNode, MacroRefNode } from './types.ts';

/** 宏展开的最大嵌套深度。 */
export const MAX_MACRO_DEPTH = 20;

/** 宏相关错误的基类。 */
export class MacroError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'MacroError';
  }
}

/** 宏循环引用错误，携带构成环的宏名链。 */
export class MacroCycleError extends MacroError {
  readonly cycle: readonly string[];

  constructor(cycle: readonly string[]) {
    super(`检测到宏循环引用: ${cycle.join(' -> ')}`);
    this.name = 'MacroCycleError';
    this.cycle = cycle;
  }
}

/** 宏嵌套超过深度上限错误。 */
export class MacroDepthError extends MacroError {
  readonly depth: number;

  constructor(depth: number) {
    super(`宏展开深度超过上限 ${MAX_MACRO_DEPTH}（当前深度 ${depth}）`);
    this.name = 'MacroDepthError';
    this.depth = depth;
  }
}

/** 引用了未定义的宏。 */
export class UndefinedMacroError extends MacroError {
  constructor(name: string) {
    super(`引用了未定义的宏: ${name}`);
    this.name = 'UndefinedMacroError';
  }
}

function cloneNode(node: ConditionNode): ConditionNode {
  switch (node.kind) {
    case 'compare':
      return { ...node };
    case 'literal':
      return { ...node };
    case 'macro':
      return { ...node };
    case 'not':
      return { kind: 'not', operand: cloneNode(node.operand) };
    case 'and':
      return { kind: 'and', operands: node.operands.map(cloneNode) };
    case 'or':
      return { kind: 'or', operands: node.operands.map(cloneNode) };
  }
}

/**
 * 展开条件 AST 中的全部宏引用。
 *
 * - 遇到已在当前展开栈中的宏即判定为循环引用，抛出 MacroCycleError；
 * - 嵌套深度（从 1 开始计数）超过 MAX_MACRO_DEPTH 抛出 MacroDepthError；
 * - 宏未定义抛出 UndefinedMacroError；
 * - 返回全新的 AST，不修改入参，展开结果中不再包含 macro 节点。
  */
export function expandMacros(
  node: ConditionNode,
  macros: Record<string, ConditionNode>,
): ConditionNode {
  const stack: string[] = [];

  const expand = (current: ConditionNode, depth: number): ConditionNode => {
    switch (current.kind) {
      case 'compare':
      case 'literal':
        return cloneNode(current);
      case 'not':
        return { kind: 'not', operand: expand(current.operand, depth) };
      case 'and':
        return { kind: 'and', operands: current.operands.map((child) => expand(child, depth)) };
      case 'or':
        return { kind: 'or', operands: current.operands.map((child) => expand(child, depth)) };
      case 'macro':
        return expandRef(current, depth);
    }
  };

  const expandRef = (ref: MacroRefNode, depth: number): ConditionNode => {
    const nextDepth = depth + 1;
    if (nextDepth > MAX_MACRO_DEPTH) {
      throw new MacroDepthError(nextDepth);
    }
    const cycleAt = stack.indexOf(ref.name);
    if (cycleAt !== -1) {
      throw new MacroCycleError([...stack.slice(cycleAt), ref.name]);
    }
    const body = macros[ref.name];
    if (body === undefined) {
      throw new UndefinedMacroError(ref.name);
    }
    stack.push(ref.name);
    try {
      return expand(body, nextDepth);
    } finally {
      stack.pop();
    }
  };

  return expand(node, 0);
}

