/**
 * 虚拟节点：挂载后每个节点获得稳定 id，
 * 补丁（Patch）通过 id 直接寻址目标节点，无需树 Diff。
 */

export type VNode = VElement | VText;

export interface VElement {
  type: 'element';
  id: number;
  tag: string;
  attrs: Record<string, string>;
  children: VNode[];
}

export interface VText {
  type: 'text';
  id: number;
  text: string;
}

/** 引擎产出的最小精准补丁操作 */
export type Patch =
  | { op: 'UpdateTextNode'; id: number; text: string }
  | { op: 'SetAttribute'; id: number; name: string; value: string }
  | { op: 'RemoveAttribute'; id: number; name: string };

/** 将补丁应用到虚拟树上（模拟真实 DOM 的局部对齐） */
export function applyPatches(
  index: Map<number, VNode>,
  patches: Patch[],
): void {
  for (const patch of patches) {
    const node = index.get(patch.id);
    if (!node) throw new Error(`Patch target #${patch.id} not found`);
    switch (patch.op) {
      case 'UpdateTextNode': {
        if (node.type !== 'text') throw new Error(`#${patch.id} is not a text node`);
        node.text = patch.text;
        break;
      }
      case 'SetAttribute': {
        if (node.type !== 'element') throw new Error(`#${patch.id} is not an element`);
        node.attrs[patch.name] = patch.value;
        break;
      }
      case 'RemoveAttribute': {
        if (node.type !== 'element') throw new Error(`#${patch.id} is not an element`);
        delete node.attrs[patch.name];
        break;
      }
    }
  }
}

/** 序列化为 HTML 字符串，便于断言与演示 */
export function renderToString(node: VNode): string {
  if (node.type === 'text') return escapeHtml(node.text);
  const attrs = Object.entries(node.attrs)
    .map(([k, v]) => ` ${k}="${escapeHtml(v)}"`)
    .join('');
  const inner = node.children.map(renderToString).join('');
  return `<${node.tag}${attrs}>${inner}</${node.tag}>`;
}

function escapeHtml(s: string): string {
  return s
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}
