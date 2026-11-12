/**
 * 最小 DOM 宿主接口。浏览器环境由 src/browser.ts 的原生 DOM 适配器实现，
 * 测试环境由 test/dom.ts 的内存 DOM 实现，核心消毒逻辑不直接依赖任何
 * 具体 DOM 库，因此可以脱离 jsdom/linkedom 运行。
 */

export const HTML_NS = 'http://www.w3.org/1999/xhtml';
export const SVG_NS = 'http://www.w3.org/2000/svg';
export const MATHML_NS = 'http://www.w3.org/1998/Math/MathML';

export const ELEMENT_NODE = 1;
export const TEXT_NODE = 3;
export const COMMENT_NODE = 8;

export interface MiniNode {
  nodeType: number;
  nodeName: string;
  parentNode: MiniNode | null;
  childNodes: MiniNode[];
  textContent: string;
  cloneNode(deep: boolean): MiniNode;
}

export interface MiniElement extends MiniNode {
  nodeType: typeof ELEMENT_NODE;
  namespaceURI: string;
  localName: string;
  /** 按存储顺序返回属性全名（测试 DOM 为限定名，浏览器为 localName）。 */
  getAttributeNames(): string[];
  getAttribute(name: string): string | null;
  setAttribute(name: string, value: string): void;
  removeAttribute(name: string): void;
}

export interface DomHost {
  createFragment(): MiniNode;
  createElement(tag: string): MiniElement;
  createTextNode(data: string): MiniNode;
  isElement(node: MiniNode): node is MiniElement;
  isText(node: MiniNode): boolean;
  appendChild(parent: MiniNode, child: MiniNode): void;
  removeChild(parent: MiniNode, child: MiniNode): void;
  /** 深度克隆，克隆体 parentNode 置空。 */
  clone(node: MiniNode): MiniNode;
  /** 把 HTML 字符串解析为文档片段。 */
  parseHTML(html: string): MiniNode;
  /** 序列化为 HTML 文本。 */
  serialize(fragment: MiniNode): string;
}

export interface SanitizeResult {
  /** 消毒后的文档片段；dropped 为 true 时为空片段。 */
  fragment: MiniNode;
  /** true 表示触发了整段丢弃策略（往返不稳定或输入无法处理）。 */
  dropped: boolean;
  /** 丢弃原因，便于编辑器埋点与调试。 */
  reason: 'ok' | 'round-trip-mismatch' | 'unsupported-input';
}
