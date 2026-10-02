/**
 * 基于树自动机的结构校验器。
 *
 * 自根向下递归：对每个节点，用其产生式编译出的 NFA 匹配子节点标签
 * 序列；匹配失败时，根据自动机的期望集/后续集生成精细诊断，并定位
 * 到出错路径（如 /book/author）。
 */
import type { Grammar, Rule } from './grammar.ts';
import { matchLabels } from './pattern.ts';
import type { MatchFailure } from './pattern.ts';
import { describePattern } from './pattern.ts';
import { childLabels, jsonToTree } from './tree.ts';
import type { TreeNode } from './tree.ts';

export interface Diagnostic {
  /** 出错位置的路径，如 /book/author */
  path: string;
  /** 精细诊断信息，如 “预期出现[name, email]，实际未找到” */
  message: string;
}

export function validateDocument(
  doc: unknown,
  grammar: Grammar,
): Diagnostic[] {
  // JSON 文档视为 { 根标签: 内容 }，取唯一顶层键作为树的根
  const wrapper = jsonToTree(doc, '$document');
  if (wrapper.children.length !== 1) {
    return [{ path: '/', message: '文档必须包含且仅包含一个根元素' }];
  }
  return validateTree(wrapper.children[0], grammar);
}

export function validateTree(root: TreeNode, grammar: Grammar): Diagnostic[] {
  const diagnostics: Diagnostic[] = [];
  if (root.label !== grammar.root) {
    diagnostics.push({
      path: `/${root.label}`,
      message: `文档根元素应为'${grammar.root}'，实际为'${root.label}'`,
    });
    return diagnostics;
  }
  validateNode(root, grammar, `/${root.label}`, diagnostics);
  return diagnostics;
}

function validateNode(
  node: TreeNode,
  grammar: Grammar,
  path: string,
  diagnostics: Diagnostic[],
): void {
  const rule = grammar.rules.get(node.label);
  if (!rule) {
    diagnostics.push({ path, message: `未定义的元素'${node.label}'` });
    return;
  }

  if (rule.pattern.kind === 'text') {
    if (node.children.length > 0) {
      diagnostics.push({
        path,
        message: `'${node.label}'应为文本节点，实际包含子元素[${childLabels(node).join(', ')}]`,
      });
    }
    return;
  }

  const result = matchLabels(rule.nfa, childLabels(node));
  if (!result.ok) {
    diagnostics.push(...buildDiagnostics(node, rule, path, result.failure, grammar));
  }

  // 递归校验每个有定义规则的子节点，收集嵌套错误
  for (const child of node.children) {
    validateNode(child, grammar, `${path}/${child.label}`, diagnostics);
  }
}

/** 依据 NFA 失败信息生成一条（或一组）精细诊断。 */
function buildDiagnostics(
  node: TreeNode,
  rule: Rule,
  path: string,
  failure: MatchFailure,
  grammar: Grammar,
): Diagnostic[] {
  const actual = childLabels(node)[failure.position];

  // 情形 1：序列提前结束 —— 末尾缺少必要元素
  if (actual === undefined) {
    return [missingDiagnostic(path, failure.expected, grammar)];
  }

  // 情形 2：实际标签属于“后续可出现”的集合，且期望的标签在后续实际
  // 序列中不再出现 —— 中间的必要元素缺失（若期望标签稍后仍出现，
  // 则属于顺序错误，落入情形 3 报“实际为...”）
  const remaining = childLabels(node).slice(failure.position + 1);
  const expectedAppearsLater = failure.expected.some((e) => remaining.includes(e));
  if (failure.expected.length > 0 && failure.future.includes(actual)
      && !failure.expected.includes(actual) && !expectedAppearsLater) {
    return [missingDiagnostic(path, failure.expected, grammar)];
  }

  // 情形 3：无法消费的意外元素
  const expectedDesc =
    failure.expected.length > 0
      ? `预期出现[${failure.expected.join(' | ')}]`
      : `预期没有更多子元素`;
  return [
    { path, message: `${expectedDesc}，实际为'${actual}'` },
  ];
}

/**
 * 缺失必要元素的诊断。
 * 若只缺一个元素且该元素有产生式，则把路径定位到缺失元素本身，
 * 并用其内容模型描述“预期出现”的内容，例如：
 *   路径 /book/author: 预期出现[name, email]，实际未找到
 */
function missingDiagnostic(
  parentPath: string,
  expected: string[],
  grammar: Grammar,
): Diagnostic {
  if (expected.length === 1) {
    const missingLabel = expected[0];
    const missingRule = grammar.rules.get(missingLabel);
    // 文本叶子元素用标签名描述，复合元素用其内容模型描述
    const contentDesc =
      !missingRule || missingRule.pattern.kind === 'text'
        ? missingLabel
        : describePattern(missingRule.pattern);
    return {
      path: `${parentPath}/${missingLabel}`,
      message: `预期出现[${contentDesc}]，实际未找到`,
    };
  }
  return {
    path: parentPath,
    message: `预期出现[${expected.join(' | ')}]，实际未找到`,
  };
}
