import type { ConditionNode, Finding } from './types.ts';

/**
 * 内核 task->comm（proc.name）固定 16 字节且末字节为 '\0'，
 * 因此用户态可见的进程名最多保留 15 个字节，超出部分被截断。
 */
export const MAX_PROC_NAME_BYTES = 15;

const EQUALITY_OPERATORS = new Set(['=', '==']);

function walk(node: ConditionNode, visit: (node: ConditionNode) => void): void {
  visit(node);
  switch (node.kind) {
    case 'not':
      walk(node.operand, visit);
      break;
    case 'and':
    case 'or':
      for (const child of node.operands) walk(child, visit);
      break;
    default:
      break;
  }
}

/**
 * 扫描条件，找出 proc.name 与超过 15 字节字面量做相等比较的节点。
 * 这类比较永远不可能为真（右值长于字段可能的最大长度），会让规则静默失效。
 */
export function checkProcNameTruncation(condition: ConditionNode): Finding[] {
  const findings: Finding[] = [];

  walk(condition, (node) => {
    if (node.kind !== 'compare') return;
    if (node.field !== 'proc.name') return;
    if (!EQUALITY_OPERATORS.has(node.operator)) return;
    if (typeof node.value !== 'string') return;

    const byteLength = Buffer.byteLength(node.value, 'utf8');
    if (byteLength > MAX_PROC_NAME_BYTES) {
      findings.push({
        code: 'proc-name-truncation',
        eventType: null,
        field: 'proc.name',
        message:
          `proc.name 在内核中最多保留 ${MAX_PROC_NAME_BYTES} 字节，` +
          `字面量 "${node.value}" 长度为 ${byteLength} 字节，` +
          `使用 ${node.operator} 比较永远不可能匹配`,
      });
    }
  });

  return findings;
}

