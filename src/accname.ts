/**
 * 简化的可访问名 / 可访问描述计算（不依赖浏览器布局引擎）。
 *
 * 名称来源优先级（高 → 低）：
 *   0. 角色禁止命名（presentation/none/paragraph/...）→ 空名（仅根节点判定）
 *   1. aria-labelledby（可跨节点链式跟随；被引用的隐藏节点也计入；
 *      用 visited 集合做循环检测；引用全部悬空或成环时回退到后续来源）
 *   2. aria-label
 *   3. 原生标签（img/input 的 alt、label[for] 关联）
 *   4. 内容派生（仅限允许从内容命名的角色，或被引用/遍历途中；
 *      未显式引用的隐藏内容跳过；嵌套控件贡献其替代文本）
 *   5. title 兜底
 *
 * 描述来源优先级：aria-describedby → aria-description → title。
 *
 * 计算过程中把读取到的节点属性/文本/结构登记为依赖键（见 tree.ts），
 * 供增量引擎在 DOM 式变更后精确失效。
 */
import { normalizeSpace, renderText } from './metrics.ts';
import type { GlyphMetrics } from './metrics.ts';
import type { NodeTree, TreeNode } from './tree.ts';

export const PROHIBITED_NAMING_ROLES: ReadonlySet<string> = new Set([
  'caption', 'code', 'definition', 'deletion', 'emphasis', 'generic',
  'insertion', 'mark', 'none', 'paragraph', 'presentation', 'strong',
  'subscript', 'superscript', 'time',
]);

export const NAME_FROM_CONTENT_ROLES: ReadonlySet<string> = new Set([
  'button', 'checkbox', 'heading', 'label', 'link', 'menuitem', 'option',
  'radio', 'switch', 'tab',
]);

export const LABELABLE_ROLES: ReadonlySet<string> = new Set([
  'checkbox', 'radio', 'select', 'slider', 'spinbutton', 'textbox',
]);

export interface DepSink {
  add(dep: string): void;
}

export interface NameAndDescription {
  name: string;
  description: string;
}

type Mode = 'root' | 'referenced' | 'content';

export function computeAccessibleName(
  tree: NodeTree,
  metrics: GlyphMetrics,
  key: string,
  deps?: DepSink,
): string {
  const node = tree.get(key);
  if (!node) return '';
  return nameFromNode(node, tree, metrics, new Set(), deps, 'root');
}

export function computeAccessibleDescription(
  tree: NodeTree,
  metrics: GlyphMetrics,
  key: string,
  deps?: DepSink,
): string {
  const node = tree.get(key);
  if (!node) return '';
  return descriptionFromAttrs(node, tree, metrics, new Set([node.key]), deps);
}

export function computeNameAndDescription(
  tree: NodeTree,
  metrics: GlyphMetrics,
  key: string,
  deps?: DepSink,
): NameAndDescription {
  return {
    name: computeAccessibleName(tree, metrics, key, deps),
    description: computeAccessibleDescription(tree, metrics, key, deps),
  };
}

function nameFromNode(
  node: TreeNode,
  tree: NodeTree,
  metrics: GlyphMetrics,
  visited: Set<string>,
  deps: DepSink | undefined,
  mode: Mode,
): string {
  if (visited.has(node.key)) return '';
  visited.add(node.key);
  deps?.add(`attr:${node.key}`);
  deps?.add(`role:${node.key}`);
  deps?.add(`hidden:${node.key}`);

  if (mode === 'root' && node.role !== null && PROHIBITED_NAMING_ROLES.has(node.role)) {
    return '';
  }
  // 隐藏且未被显式引用的内容不参与命名；被引用（referenced）的隐藏节点照常计入。
  if (node.hidden && mode === 'content') return '';

  if (mode !== 'content') {
    const labelledby = node.attrs['aria-labelledby'];
    if (labelledby !== undefined && labelledby.trim() !== '') {
      const parts: string[] = [];
      for (const id of labelledby.trim().split(/\s+/)) {
        deps?.add(`idref:${id}`);
        const target = tree.byId(id);
        if (!target) continue; // 悬空引用：贡献为空，不抛错
        const part = nameFromNode(target, tree, metrics, visited, deps, 'referenced');
        if (part) parts.push(part);
      }
      const joined = normalizeSpace(parts.join(' '));
      if (joined) return joined;
      // 引用全部悬空或构成回环：回退到后续来源。
    }
  }

  const ariaLabel = node.attrs['aria-label'];
  if (ariaLabel !== undefined) {
    const label = normalizeSpace(renderText(ariaLabel, metrics));
    if (label) return label;
  }

  const native = nativeLabel(node, tree, metrics, visited, deps);
  if (native) return native;

  if (mode !== 'root' || (node.role !== null && NAME_FROM_CONTENT_ROLES.has(node.role))) {
    const fromContent = contentText(node, tree, metrics, visited, deps);
    if (fromContent) return fromContent;
  }

  const title = node.attrs['title'];
  if (title !== undefined) {
    const fromTitle = normalizeSpace(renderText(title, metrics));
    if (fromTitle) return fromTitle;
  }
  return '';
}

function nativeLabel(
  node: TreeNode,
  tree: NodeTree,
  metrics: GlyphMetrics,
  visited: Set<string>,
  deps: DepSink | undefined,
): string {
  const alt = node.attrs['alt'];
  if (alt !== undefined && (node.role === 'img' || node.role === 'image' || node.role === 'input')) {
    return normalizeSpace(renderText(alt, metrics));
  }
  if (node.id !== null && node.role !== null && LABELABLE_ROLES.has(node.role)) {
    deps?.add('labelfor');
    const label = tree.findByAttr('for', node.id);
    if (label) {
      return nameFromNode(label, tree, metrics, visited, deps, 'referenced');
    }
  }
  return '';
}

function contentText(
  node: TreeNode,
  tree: NodeTree,
  metrics: GlyphMetrics,
  visited: Set<string>,
  deps: DepSink | undefined,
): string {
  deps?.add(`text:${node.key}`);
  deps?.add(`children:${node.key}`);
  const parts: string[] = [];
  if (node.text) {
    const own = normalizeSpace(renderText(node.text, metrics));
    if (own) parts.push(own);
  }
  for (const child of node.children) {
    const part = nameFromNode(child, tree, metrics, visited, deps, 'content');
    if (part) parts.push(part);
  }
  return normalizeSpace(parts.join(' '));
}

function descriptionFromAttrs(
  node: TreeNode,
  tree: NodeTree,
  metrics: GlyphMetrics,
  visited: Set<string>,
  deps: DepSink | undefined,
): string {
  deps?.add(`attr:${node.key}`);
  const describedby = node.attrs['aria-describedby'];
  if (describedby !== undefined && describedby.trim() !== '') {
    const parts: string[] = [];
    for (const id of describedby.trim().split(/\s+/)) {
      deps?.add(`idref:${id}`);
      const target = tree.byId(id);
      if (!target) continue;
      const part = descriptionFromNode(target, tree, metrics, visited, deps);
      if (part) parts.push(part);
    }
    const joined = normalizeSpace(parts.join(' '));
    if (joined) return joined;
  }
  const ariaDescription = node.attrs['aria-description'];
  if (ariaDescription !== undefined) {
    const direct = normalizeSpace(renderText(ariaDescription, metrics));
    if (direct) return direct;
  }
  const title = node.attrs['title'];
  if (title !== undefined) {
    const fromTitle = normalizeSpace(renderText(title, metrics));
    if (fromTitle) return fromTitle;
  }
  return '';
}

function descriptionFromNode(
  node: TreeNode,
  tree: NodeTree,
  metrics: GlyphMetrics,
  visited: Set<string>,
  deps: DepSink | undefined,
): string {
  if (visited.has(node.key)) return '';
  visited.add(node.key);
  // 被显式引用的节点即使隐藏也贡献描述文本。
  const chained = descriptionFromAttrs(node, tree, metrics, visited, deps);
  if (chained) return chained;
  return contentText(node, tree, metrics, visited, deps);
}
