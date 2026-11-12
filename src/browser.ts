import {
  ELEMENT_NODE,
  TEXT_NODE,
  type DomHost,
  type MiniElement,
  type MiniNode,
} from './types.ts';

/**
 * 基于浏览器原生 DOM 的宿主适配器。解析与序列化都走同一份 HTML
 * 解析器（template 元素），因此往返校验覆盖的是编辑器真实会用到的
 * 解析行为，而不是消毒器自己的另一套实现。
 */
export function createBrowserDomHost(global: typeof globalThis = globalThis): DomHost {
  const document = global.document;
  const template = document.createElement('template');

  return {
    createFragment(): MiniNode {
      return document.createDocumentFragment() as unknown as MiniNode;
    },
    createElement(tag: string): MiniElement {
      return document.createElement(tag) as unknown as MiniElement;
    },
    createTextNode(data: string): MiniNode {
      return document.createTextNode(data) as unknown as MiniNode;
    },
    isElement(node: MiniNode): node is MiniElement {
      return node.nodeType === ELEMENT_NODE;
    },
    isText(node: MiniNode): boolean {
      return node.nodeType === TEXT_NODE;
    },
    appendChild(parent: MiniNode, child: MiniNode): void {
      (parent as Node).appendChild(child as Node);
    },
    removeChild(parent: MiniNode, child: MiniNode): void {
      (parent as Node).removeChild(child as Node);
    },
    clone(node: MiniNode): MiniNode {
      return (node as Node).cloneNode(true) as unknown as MiniNode;
    },
    parseHTML(html: string): MiniNode {
      const holder = template.cloneNode(false) as HTMLTemplateElement;
      holder.innerHTML = html;
      const fragment = document.createDocumentFragment();
      while (holder.content.firstChild) {
        fragment.appendChild(holder.content.firstChild);
      }
      return fragment as unknown as MiniNode;
    },
    serialize(fragment: MiniNode): string {
      const holder = template.cloneNode(false) as HTMLTemplateElement;
      while (fragment.childNodes.length > 0) {
        holder.content.appendChild(fragment.childNodes[0] as Node);
      }
      const html = holder.innerHTML;
      // 把节点放回调用者的片段，避免 serialize 产生可观察的副作用。
      const fragmentNode = fragment as unknown as DocumentFragment;
      while (holder.content.firstChild) {
        fragmentNode.appendChild(holder.content.firstChild);
      }
      return html;
    },
  };
}
