/**
 * 通用标签树：XML/JSON 文档的统一抽象。
 *
 * JSON -> 树的映射规则：
 *   { "a": v }          -> 标签为 a 的节点，其子节点由 v 递归得到
 *   [v1, v2, ...]       -> 同一标签的多个兄弟节点（重复元素）
 *   字符串/数字/布尔     -> 文本叶子
 */
export interface TreeNode {
  label: string;
  text?: string;
  children: TreeNode[];
}

export function jsonToTree(value: unknown, label: string): TreeNode {
  if (Array.isArray(value)) {
    // 由调用方展开；此处防御性处理
    const node: TreeNode = { label, children: [] };
    for (const item of value) {
      node.children.push(jsonToTree(item, label));
    }
    return node;
  }
  if (value !== null && typeof value === 'object') {
    const node: TreeNode = { label, children: [] };
    for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
      if (Array.isArray(child)) {
        for (const item of child) node.children.push(jsonToTree(item, key));
      } else {
        node.children.push(jsonToTree(child, key));
      }
    }
    return node;
  }
  return { label, text: String(value), children: [] };
}

/** 子节点的标签序列（水平自动机的输入）。 */
export function childLabels(node: TreeNode): string[] {
  return node.children.map((c) => c.label);
}
