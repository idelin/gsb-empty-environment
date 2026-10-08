/**
 * Concrete syntax tree. Every node owns an exact byte range of the source so
 * that `reprint(source, root) === source` (comments, blank lines and original
 * newlines included). Children of any container tile it contiguously.
 */

export interface TriviaNode {
  kind: 'trivia';
  start: number;
  end: number;
}

export interface LeafNode {
  kind: 'leaf';
  start: number;
  end: number; // whole line, terminator included
  key: string;
  keyStart: number;
  keyEnd: number;
  value: string;
  valueStart: number;
  valueEnd: number;
}

export interface BlockNode {
  kind: 'block';
  start: number; // start of the header line
  headerEnd: number; // end of the header line, terminator included
  end: number; // end of the last descendant (or headerEnd when empty)
  key: string;
  keyStart: number;
  keyEnd: number;
  children: CstNode[];
}

export interface RootNode {
  kind: 'root';
  start: 0;
  end: number;
  children: CstNode[];
}

export type CstNode = TriviaNode | LeafNode | BlockNode;
export type Container = RootNode | BlockNode;

export function reprint(source: string, node: CstNode | RootNode): string {
  switch (node.kind) {
    case 'trivia':
    case 'leaf':
      return source.slice(node.start, node.end);
    case 'block': {
      let out = source.slice(node.start, node.headerEnd);
      for (const child of node.children) out += reprint(source, child);
      return out;
    }
    case 'root': {
      let out = '';
      for (const child of node.children) out += reprint(source, child);
      return out;
    }
  }
}

export function findChild(
  container: Container,
  key: string,
): LeafNode | BlockNode | undefined {
  for (const child of container.children) {
    if ((child.kind === 'leaf' || child.kind === 'block') && child.key === key) {
      return child;
    }
  }
  return undefined;
}

export function findPath(
  root: RootNode,
  path: string[],
): LeafNode | BlockNode | undefined {
  let current: Container = root;
  let found: LeafNode | BlockNode | undefined;
  for (const key of path) {
    found = findChild(current, key);
    if (!found) return undefined;
    if (found.kind === 'block') current = found;
  }
  return found;
}
