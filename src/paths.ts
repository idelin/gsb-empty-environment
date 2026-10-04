/**
 * 资源路径工具：前缀匹配、按目录展开、集合压缩。
 *
 * 路径形如 `/eng/docs`，根为 `/`。
 * 压缩输出中 `/**` 后缀表示整个子树（含自身），否则为单条具体路径。
 */

export function normalizePath(path: string): string {
  if (!path.startsWith('/')) path = '/' + path;
  while (path.length > 1 && path.endsWith('/')) path = path.slice(0, -1);
  return path;
}

/** prefix 是否覆盖 path（自身或祖先目录）。 */
export function prefixCovers(prefix: string, path: string): boolean {
  prefix = normalizePath(prefix);
  path = normalizePath(path);
  if (prefix === '/') return true;
  return path === prefix || path.startsWith(prefix + '/');
}

/** 把授权/例外上的路径前缀展开为目录中的具体资源路径。 */
export function expandPrefix(prefix: string, catalogPaths: readonly string[]): Set<string> {
  const out = new Set<string>();
  for (const p of catalogPaths) {
    if (prefixCovers(prefix, p)) out.add(normalizePath(p));
  }
  return out;
}

interface TrieNode {
  segment: string;
  path: string;
  isResource: boolean;
  children: Map<string, TrieNode>;
}

function buildTrie(catalogPaths: readonly string[]): TrieNode {
  const root: TrieNode = { segment: '', path: '/', isResource: false, children: new Map() };
  for (const raw of catalogPaths) {
    const path = normalizePath(raw);
    if (path === '/') {
      root.isResource = true;
      continue;
    }
    const segments = path.slice(1).split('/');
    let node = root;
    let current = '';
    for (const seg of segments) {
      current += '/' + seg;
      let child = node.children.get(seg);
      if (!child) {
        child = { segment: seg, path: current, isResource: false, children: new Map() };
        node.children.set(seg, child);
      }
      node = child;
    }
    node.isResource = true;
  }
  return root;
}

/**
 * 将具体路径集合压缩为最短的前缀表示。
 * 当某节点自身（若是资源）及其全部后代都在集合中时，折叠为 `<path>/**`。
 */
export function compressPaths(paths: ReadonlySet<string>, catalogPaths: readonly string[]): string[] {
  const wanted = new Set<string>();
  for (const p of paths) wanted.add(normalizePath(p));
  const root = buildTrie(catalogPaths);

  const fullCover = (node: TrieNode): boolean => {
    if (node.isResource && !wanted.has(node.path)) return false;
    for (const child of node.children.values()) {
      if (!fullCover(child)) return false;
    }
    return node.isResource || node.children.size > 0;
  };

  const out: string[] = [];
  const emit = (node: TrieNode, isRoot: boolean): void => {
    if (fullCover(node)) {
      // 叶子资源（无子节点）直接输出精确路径，带子树的节点折叠为 /**
      if (isRoot) out.push('/**');
      else if (node.children.size > 0) out.push(node.path + '/**');
      else out.push(node.path);
      return;
    }
    if (node.isResource && wanted.has(node.path)) out.push(node.path);
    for (const child of [...node.children.values()].sort((a, b) => a.path.localeCompare(b.path))) {
      emit(child, false);
    }
  };
  emit(root, true);
  return out.sort();
}

/** 判断压缩模式（可能带 `/**` 后缀）是否覆盖某具体路径。 */
export function patternCovers(pattern: string, path: string): boolean {
  if (pattern.endsWith('/**')) return prefixCovers(pattern.slice(0, -3) || '/', path);
  return normalizePath(pattern) === normalizePath(path);
}
