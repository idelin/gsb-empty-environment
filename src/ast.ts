/**
 * 正则抽象语法树。
 * 支持的构造：字面字符、连接、或（|）、星号（*）、捕获括号（()）。
 * 每个节点带唯一 id，供匹配器做记忆化。
 */
export type Ast =
  | { kind: 'char'; id: number; ch: string }
  | { kind: 'concat'; id: number; parts: Ast[] } // parts 为空时表示空串（epsilon）
  | { kind: 'alt'; id: number; options: Ast[] }
  | { kind: 'star'; id: number; child: Ast }
  | { kind: 'group'; id: number; index: number; child: Ast }; // index 从 1 开始

/** 捕获组数量（group 节点的最大 index）。 */
export function countGroups(node: Ast): number {
  switch (node.kind) {
    case 'char':
      return 0;
    case 'concat':
      return node.parts.reduce((n, p) => Math.max(n, countGroups(p)), 0);
    case 'alt':
      return node.options.reduce((n, o) => Math.max(n, countGroups(o)), 0);
    case 'star':
      return countGroups(node.child);
    case 'group':
      return Math.max(node.index, countGroups(node.child));
  }
}

/** 打印回模式串（用于解析器往返测试）。优先级：star > concat > alt。 */
export function print(node: Ast): string {
  switch (node.kind) {
    case 'char':
      return node.ch;
    case 'concat':
      return node.parts.map(printConcatItem).join('');
    case 'alt':
      return node.options.map(printAltItem).join('|');
    case 'star':
      return printStarOperand(node.child) + '*';
    case 'group':
      return '(' + print(node.child) + ')';
  }
}

// 或的分支是连接级：嵌套的或需要括号
function printAltItem(node: Ast): string {
  return node.kind === 'alt' ? '(' + print(node) + ')' : print(node);
}

// 连接的成员是重复级：char/group/star 可直接打印，其余需要括号
function printConcatItem(node: Ast): string {
  switch (node.kind) {
    case 'char':
    case 'group':
    case 'star':
      return print(node);
    default:
      return '(' + print(node) + ')';
  }
}

// 星号的操作数是原子级：只有 char/group 可直接打印；
// 单元素连接可展开，空连接（epsilon）需要括号保住语义
function printStarOperand(node: Ast): string {
  switch (node.kind) {
    case 'char':
    case 'group':
      return print(node);
    case 'concat':
      return node.parts.length === 1 ? printStarOperand(node.parts[0]!) : '(' + print(node) + ')';
    default:
      return '(' + print(node) + ')';
  }
}
