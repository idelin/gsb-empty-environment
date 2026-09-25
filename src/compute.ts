import type { DepKey, GlyphMetrics, NameResult, TreeNode } from './types.ts';

export interface ComputeContext {
  index: Map<string, TreeNode>;
  metrics: GlyphMetrics;
  deps: Set<DepKey>;
}

export const NAME_PROHIBITED_ROLES = new Set(['presentation', 'none', 'generic']);

const EMBEDDED_CONTROL_ROLES = new Set([
  'img',
  'textbox',
  'checkbox',
  'radio',
  'switch',
  'slider',
  'spinbutton',
  'combobox',
]);

export function buildIndex(root: TreeNode): Map<string, TreeNode> {
  const index = new Map<string, TreeNode>();
  const walk = (node: TreeNode): void => {
    if (!index.has(node.id)) index.set(node.id, node);
    for (const child of node.children) walk(child);
  };
  walk(root);
  return index;
}

function normalize(text: string): string {
  return text.replace(/\s+/g, ' ').trim();
}

function join(acc: string, part: string): string {
  const piece = normalize(part);
  if (!piece) return acc;
  return acc ? `${acc} ${piece}` : piece;
}

function splitIds(value: string | undefined): string[] {
  return value ? value.trim().split(/\s+/).filter(Boolean) : [];
}

function isRendered(node: TreeNode, ctx: ComputeContext): boolean {
  const charWidth = ctx.metrics.overrides?.[node.id] ?? ctx.metrics.charWidth;
  return (node.text?.length ?? 0) * charWidth > 0;
}

function embeddedAlternative(node: TreeNode, ctx: ComputeContext): string {
  ctx.deps.add(`${node.id}:role`);
  ctx.deps.add(`${node.id}:attr:alt`);
  ctx.deps.add(`${node.id}:attr:value`);
  return node.attrs['alt'] ?? node.attrs['value'] ?? '';
}

function contentText(
  node: TreeNode,
  ctx: ComputeContext,
  visited: Set<string>,
  referenced: boolean,
): string {
  ctx.deps.add(`${node.id}:hidden`);
  ctx.deps.add(`${node.id}:text`);
  ctx.deps.add(`children:${node.id}`);
  let out = '';
  const suppressed = Boolean(node.hidden) && !referenced;
  if (!suppressed && node.text && isRendered(node, ctx)) out = join(out, node.text);
  for (const child of node.children) {
    out = join(out, childText(child, ctx, visited, referenced));
  }
  return out;
}

function childText(
  node: TreeNode,
  ctx: ComputeContext,
  visited: Set<string>,
  referenced: boolean,
): string {
  ctx.deps.add(`${node.id}:hidden`);
  if (node.hidden && !referenced) return '';
  if (EMBEDDED_CONTROL_ROLES.has(node.role ?? '')) return embeddedAlternative(node, ctx);
  if (visited.has(node.id)) return '';
  visited.add(node.id);
  return contentText(node, ctx, visited, referenced);
}

function referencedText(node: TreeNode, ctx: ComputeContext, visited: Set<string>): string {
  if (visited.has(node.id)) return '';
  visited.add(node.id);
  if (EMBEDDED_CONTROL_ROLES.has(node.role ?? '')) return embeddedAlternative(node, ctx);
  ctx.deps.add(`${node.id}:attr:aria-labelledby`);
  const chained = labelledbyText(node, ctx, visited);
  if (chained) return chained;
  ctx.deps.add(`${node.id}:attr:aria-label`);
  const ariaLabel = node.attrs['aria-label'];
  if (ariaLabel && ariaLabel.trim()) return normalize(ariaLabel);
  return contentText(node, ctx, visited, true);
}

function labelledbyText(node: TreeNode, ctx: ComputeContext, visited: Set<string>): string {
  let out = '';
  for (const ref of splitIds(node.attrs['aria-labelledby'])) {
    ctx.deps.add(`@id:${ref}`);
    const target = ctx.index.get(ref);
    if (!target) continue;
    out = join(out, referencedText(target, ctx, visited));
  }
  return out;
}

function nativeLabelText(nodeId: string, ctx: ComputeContext): string {
  ctx.deps.add(`@labelfor:${nodeId}`);
  let out = '';
  for (const candidate of ctx.index.values()) {
    if (candidate.role === 'label' && candidate.attrs['for'] === nodeId) {
      out = join(out, contentText(candidate, ctx, new Set([nodeId, candidate.id]), false));
    }
  }
  return out;
}

export function computeName(nodeId: string, ctx: ComputeContext): string {
  const node = ctx.index.get(nodeId);
  if (!node) return '';
  ctx.deps.add(`${nodeId}:role`);
  if (NAME_PROHIBITED_ROLES.has(node.role ?? '')) return '';
  ctx.deps.add(`${nodeId}:attr:aria-labelledby`);
  const visited = new Set<string>([nodeId]);
  const fromRefs = labelledbyText(node, ctx, visited);
  if (fromRefs) return normalize(fromRefs);
  ctx.deps.add(`${nodeId}:attr:aria-label`);
  const ariaLabel = node.attrs['aria-label'];
  if (ariaLabel && ariaLabel.trim()) return normalize(ariaLabel);
  const native = nativeLabelText(nodeId, ctx);
  if (native) return normalize(native);
  if ((node.role ?? '') === 'img') {
    ctx.deps.add(`${nodeId}:attr:alt`);
    return normalize(node.attrs['alt'] ?? '');
  }
  return normalize(contentText(node, ctx, visited, false));
}

export function computeDescription(nodeId: string, ctx: ComputeContext): string {
  const node = ctx.index.get(nodeId);
  if (!node) return '';
  ctx.deps.add(`${nodeId}:attr:aria-describedby`);
  const ids = splitIds(node.attrs['aria-describedby']);
  if (ids.length > 0) {
    const visited = new Set<string>([nodeId]);
    let out = '';
    for (const ref of ids) {
      ctx.deps.add(`@id:${ref}`);
      const target = ctx.index.get(ref);
      if (!target) continue;
      out = join(out, referencedText(target, ctx, visited));
    }
    if (out) return normalize(out);
  }
  ctx.deps.add(`${nodeId}:attr:aria-description`);
  return normalize(node.attrs['aria-description'] ?? '');
}

export function fullCompute(root: TreeNode, metrics: GlyphMetrics): Map<string, NameResult> {
  const index = buildIndex(root);
  const results = new Map<string, NameResult>();
  for (const id of index.keys()) {
    const ctx: ComputeContext = { index, metrics, deps: new Set<DepKey>() };
    results.set(id, {
      name: computeName(id, ctx),
      description: computeDescription(id, ctx),
    });
  }
  return results;
}
