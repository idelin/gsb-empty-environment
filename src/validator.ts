/**
 * 自顶向下树自动机校验器：
 * 从根状态出发，逐节点检查标签规则与子节点序列正规式，
 * 收集全部违反模式的位置（路径）与精细诊断信息。
 */
import { checkSequence } from './regex.ts';
import type { Regex } from './regex.ts';
import type { Schema } from './schema.ts';
import type { TreeNode } from './tree.ts';

export interface Violation {
  /** 违反模式的位置，如 /book/author */
  path: string;
  /** 精细诊断，如 预期出现[name, email]，实际未找到 */
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  violations: Violation[];
}

export function validate(schema: Schema, tree: TreeNode): ValidationResult {
  const violations: Violation[] = [];
  if (tree.label !== schema.root) {
    violations.push({
      path: `/${tree.label}`,
      message: `根标签不匹配：预期 '${schema.root}'，实际为 '${tree.label}'`,
    });
    return { valid: false, violations };
  }
  visit(schema, tree, `/${tree.label}`, violations);
  return { valid: violations.length === 0, violations };
}

function visit(schema: Schema, tree: TreeNode, path: string, violations: Violation[]): void {
  const nodeRule = schema.lookup(tree.label);
  if (!nodeRule) {
    violations.push({ path, message: `未定义的标签 '${tree.label}'，模式中没有对应规则` });
    return;
  }

  checkText(nodeRule.text, tree, path, violations);
  checkChildren(nodeRule.children, tree, path, violations);

  const totals = new Map<string, number>();
  for (const child of tree.children) {
    totals.set(child.label, (totals.get(child.label) ?? 0) + 1);
  }
  const seen = new Map<string, number>();
  for (const child of tree.children) {
    const index = (seen.get(child.label) ?? 0) + 1;
    seen.set(child.label, index);
    const suffix = (totals.get(child.label) ?? 0) > 1 ? `${child.label}[${index}]` : child.label;
    visit(schema, child, `${path}/${suffix}`, violations);
  }
}

function checkText(
  policy: 'required' | 'optional' | 'forbidden',
  tree: TreeNode,
  path: string,
  violations: Violation[],
): void {
  const hasText = tree.text !== undefined && tree.text.trim().length > 0;
  if (policy === 'required' && !hasText) {
    violations.push({ path, message: `预期包含文本内容，实际未找到` });
  }
  if (policy === 'forbidden' && hasText) {
    violations.push({ path, message: `不允许文本内容，实际为 '${tree.text}'` });
  }
}

function checkChildren(
  pattern: Regex,
  tree: TreeNode,
  path: string,
  violations: Violation[],
): void {
  const labels = tree.children.map((child) => child.label);
  const error = checkSequence(pattern, labels);
  if (!error) {
    return;
  }
  if (error.kind === 'missing') {
    violations.push({
      path,
      message: `预期出现[${error.expected.join(', ')}]，实际未找到`,
    });
    return;
  }
  const actual = tree.children[error.index];
  const message =
    error.expected.length > 0
      ? `预期出现[${error.expected.join(', ')}]，实际出现 '${actual.label}'`
      : `此处不允许再出现子节点，实际出现 '${actual.label}'`;
  violations.push({ path: `${path}/${actual.label}`, message });
}
