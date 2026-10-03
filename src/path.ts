/**
 * JSON 字段路径解析与匹配（前缀树）。
 *
 * 路径语法：
 *   user.address.city   对象字段逐级下钻
 *   items[].sku         数组通配（对数组每个元素取 sku）
 *   items[0].sku        数组定向下标
 */

export type PathSegment =
  | { kind: 'field'; name: string }
  | { kind: 'index'; index: number }
  | { kind: 'wildcard' };

const SEGMENT_RE = /^([^\[\]]*)((?:\[\d*\])*)$/;

export function parsePath(path: string): PathSegment[] {
  const segments: PathSegment[] = [];
  if (path === '') return segments;
  for (const part of path.split('.')) {
    const m = SEGMENT_RE.exec(part);
    if (!m || (m[1] === '' && m[2] === '')) {
      throw new Error(`Invalid path segment: "${part}" in path "${path}"`);
    }
    if (m[1] !== '') segments.push({ kind: 'field', name: m[1] });
    for (const bracket of m[2].match(/\[\d*\]/g) ?? []) {
      const inner = bracket.slice(1, -1);
      if (inner === '') segments.push({ kind: 'wildcard' });
      else segments.push({ kind: 'index', index: Number(inner) });
    }
  }
  return segments;
}

/** 前缀树节点：描述“当前子树内还有哪些路径被请求”。 */
export class PathTrieNode {
  readonly children = new Map<string, PathTrieNode>();
  readonly indexChildren = new Map<number, PathTrieNode>();
  wildcardChild: PathTrieNode | undefined;
  /** 为 true 表示某条被请求路径恰好终止于此，需物化该处的值（标量或整个子树）。 */
  terminal = false;
}

export function buildTrie(paths: string[]): PathTrieNode {
  const root = new PathTrieNode();
  for (const path of paths) {
    let node = root;
    for (const seg of parsePath(path)) {
      if (seg.kind === 'field') {
        let next = node.children.get(seg.name);
        if (!next) {
          next = new PathTrieNode();
          node.children.set(seg.name, next);
        }
        node = next;
      } else if (seg.kind === 'index') {
        let next = node.indexChildren.get(seg.index);
        if (!next) {
          next = new PathTrieNode();
          node.indexChildren.set(seg.index, next);
        }
        node = next;
      } else {
        if (!node.wildcardChild) node.wildcardChild = new PathTrieNode();
        node = node.wildcardChild;
      }
    }
    node.terminal = true;
  }
  return root;
}

export interface ArrayMatch {
  node: PathTrieNode;
  via: 'wildcard' | 'index';
}

/**
 * 数组元素下标匹配，返回 0~2 个命中：
 * 定向下标（如 items[0]）与通配（items[]）可能同时命中同一元素，
 * 此时两者都返回，由调用方分别以各自路径标签处理。
 */
export function matchArrayElement(node: PathTrieNode, index: number): ArrayMatch[] {
  const matches: ArrayMatch[] = [];
  const byIndex = node.indexChildren.get(index);
  if (byIndex) matches.push({ node: byIndex, via: 'index' });
  if (node.wildcardChild) matches.push({ node: node.wildcardChild, via: 'wildcard' });
  return matches;
}

export function joinField(base: string, field: string): string {
  return base === '' ? field : `${base}.${field}`;
}
