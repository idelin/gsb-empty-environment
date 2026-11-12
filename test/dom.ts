/**
 * 仅供测试使用的最小内存 DOM。刻意实现 HTML5 解析规范中与本次安全
 * 属性相关的几条语义，以便在没有 jsdom 的离线环境里复现真实浏览器的
 * 命名空间混淆与往返行为：
 *
 * 1. 进入 svg/math 元素后切换到对应命名空间，内部标签按限定名解析；
 * 2. foreignObject 等标签把插桩模式切回 HTML（命名空间切换攻击面）；
 * 3. HTML breakout 标签从 foreign 模式退回 HTML 命名空间；
 * 4. table 内的非表格内容按 foster parenting 规则移出表格；
 * 5. 实体/数字字符引用解码，属性做同样处理；
 * 6. 序列化器按 HTML void 元素、foreign 限定名、文本转义规则输出。
 */
import {
  COMMENT_NODE,
  ELEMENT_NODE,
  HTML_NS,
  MATHML_NS,
  SVG_NS,
  TEXT_NODE,
  type DomHost,
  type MiniElement,
  type MiniNode,
} from '../src/types.ts';

const HTML_VOID_ELEMENTS = new Set([
  'area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input',
  'link', 'meta', 'param', 'source', 'track', 'wbr',
]);

const HTML_RAW_TEXT_ELEMENTS = new Set(['script', 'style', 'textarea', 'title']);

/** foreign content 中遇到这些 HTML 标签会 break out 回 HTML 命名空间。 */
const BREAKOUT_TAGS = new Set([
  'b', 'big', 'blockquote', 'body', 'br', 'center', 'code', 'dd', 'div',
  'dl', 'dt', 'em', 'embed', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'head',
  'hr', 'i', 'img', 'li', 'listing', 'menu', 'meta', 'nobr', 'ol', 'p',
  'pre', 'ruby', 's', 'small', 'span', 'strong', 'strike', 'sub', 'sup',
  'table', 'tt', 'u', 'ul', 'var',
]);

const HTML_INTEGRATION_POINTS = new Set(['foreignobject', 'desc', 'title']);

const MATHML_TEXT_INTEGRATION = new Set(['mi', 'mo', 'mn', 'ms', 'mtext']);

const TABLE_CONTENT_TAGS = new Set([
  'table', 'tbody', 'tfoot', 'thead', 'tr', 'td', 'th',
  'caption', 'colgroup', 'col',
]);

/** 只有这些容器内部的非表格内容才需要 foster 移出。 */
const FOSTER_SCOPE_TAGS = new Set([
  'table', 'tbody', 'tfoot', 'thead', 'tr', 'colgroup',
]);

const NAMED_ENTITIES: Record<string, string> = {
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
  nbsp: '\u00A0',
  copy: '\u00A9',
  reg: '\u00AE',
  trade: '\u2122',
  hellip: '\u2026',
  lsquo: '\u2018',
  rsquo: '\u2019',
  ldquo: '\u201C',
  rdquo: '\u201D',
};

function decodeEntities(input: string): string {
  return input.replace(/&(#x?[0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]*);?/g, (match, body: string) => {
    if (body[0] === '#') {
      const isHex = body[1] === 'x' || body[1] === 'X';
      const code = Number.parseInt(body.slice(isHex ? 2 : 1), isHex ? 16 : 10);
      if (Number.isFinite(code)) {
        return String.fromCodePoint(code);
      }
      return match;
    }
    return NAMED_ENTITIES[body.toLowerCase()] ?? match;
  });
}

class MemoryNode implements MiniNode {
  nodeType: number;
  nodeName: string;
  parentNode: MiniNode | null = null;
  childNodes: MiniNode[] = [];
  textContent = '';

  constructor(nodeType: number, nodeName: string) {
    this.nodeType = nodeType;
    this.nodeName = nodeName;
  }

  append(child: MiniNode): void {
    child.parentNode = this;
    this.childNodes.push(child);
  }

  cloneNode(deep: boolean): MiniNode {
    const copy = new MemoryNode(this.nodeType, this.nodeName);
    copy.textContent = this.textContent;
    if (deep) {
      for (const child of this.childNodes) {
        copy.append(child.cloneNode(true));
      }
    }
    return copy;
  }
}

class MemoryElement extends MemoryNode implements MiniElement {
  namespaceURI: string;
  localName: string;
  private attributes = new Map<string, string>();

  constructor(namespaceURI: string, qualifiedName: string) {
    super(ELEMENT_NODE, qualifiedName);
    this.namespaceURI = namespaceURI;
    const colon = qualifiedName.indexOf(':');
    this.localName = colon === -1 ? qualifiedName : qualifiedName.slice(colon + 1);
  }

  getAttributeNames(): string[] {
    return [...this.attributes.keys()];
  }

  getAttribute(name: string): string | null {
    return this.attributes.has(name) ? this.attributes.get(name)! : null;
  }

  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value);
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name);
  }

  override cloneNode(deep: boolean): MiniNode {
    const copy = new MemoryElement(this.namespaceURI, this.nodeName);
    for (const [name, value] of this.attributes) {
      copy.setAttribute(name, value);
    }
    if (deep) {
      for (const child of this.childNodes) {
        copy.append(child.cloneNode(true));
      }
    }
    return copy;
  }
}

class MemoryFragment extends MemoryNode {
  constructor() {
    super(11, '#document-fragment');
  }
}

export type { MemoryElement, MemoryNode };

interface TagToken {
  tagName: string;
  attributes: Array<[string, string]>;
  selfClosing: boolean;
  closing: boolean;
}

interface StackEntry {
  element: MemoryElement;
  namespace: string;
  inTable: boolean;
}

function textNode(data: string): MemoryNode {
  const node = new MemoryNode(TEXT_NODE, '#text');
  node.textContent = decodeEntities(data);
  return node;
}

function findTagEnd(html: string, start: number): number {
  let quote: string | null = null;
  for (let i = start + 1; i < html.length; i += 1) {
    const char = html[i];
    if (quote !== null) {
      if (char === quote) {
        quote = null;
      }
    } else if (char === '"' || char === "'") {
      quote = char;
    } else if (char === '>') {
      return i;
    }
  }
  return -1;
}

function parseTag(inner: string, closing: boolean): TagToken {
  const selfClosing = !closing && inner.endsWith('/');
  const body = selfClosing ? inner.slice(0, -1) : inner;
  const match = body.match(/^([^\s/>]+)([\s\S]*)$/);
  const tagName = (match ? match[1] : body).toLowerCase();
  const rest = match ? match[2] : '';
  const attributes: Array<[string, string]> = [];
  const pattern = /([^\s=/>]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+)))?/g;
  let found: RegExpExecArray | null;
  while ((found = pattern.exec(rest)) !== null) {
    const name = found[1].toLowerCase();
    if (name.length === 0 || name === '/') {
      continue;
    }
    const value = decodeEntities(found[2] ?? found[3] ?? found[4] ?? '');
    attributes.push([name, value]);
  }
  return { tagName, attributes, selfClosing, closing };
}

interface TokenizerState {
  html: string;
  index: number;
}

function nextToken(
  state: TokenizerState,
  stack: StackEntry[],
): MemoryNode | TagToken | null {
  const { html } = state;
  while (state.index < html.length) {
    const index = state.index;
    const rawContext = currentRawTextContext(stack);
    if (rawContext !== null) {
      const closeTag = `</${rawContext}`;
      const closeIndex = html.toLowerCase().indexOf(closeTag, index);
      if (closeIndex === -1) {
        state.index = html.length;
        return rawTextToken(html.slice(index));
      } else {
        const closeEnd = html.indexOf('>', closeIndex);
        const end = closeEnd === -1 ? html.length : closeEnd + 1;
        if (closeIndex > index) {
          state.index = closeIndex;
          return rawTextToken(html.slice(index, closeIndex));
        }
        const inner = html.slice(closeIndex + 2, end === html.length ? html.length : end - 1);
        state.index = end;
        return parseTag(inner, true);
      }
    }
    const nextTag = html.indexOf('<', index);
    if (nextTag === -1) {
      state.index = html.length;
      return textNode(html.slice(index));
    }
    if (nextTag > index) {
      state.index = nextTag;
      return textNode(html.slice(index, nextTag));
    }
    const after = html[nextTag + 1];
    if (after === '!') {
      if (html.startsWith('<!--', nextTag)) {
        const endComment = html.indexOf('-->', nextTag + 4);
        const stop = endComment === -1 ? html.length : endComment + 3;
        const comment = new MemoryNode(
          COMMENT_NODE,
          html.slice(nextTag + 4, endComment === -1 ? html.length : endComment),
        );
        state.index = stop;
        return comment;
      } else {
        const close = html.indexOf('>', nextTag);
        state.index = close === -1 ? html.length : close + 1;
        continue;
      }
    }
    if (after === '?') {
      const close = html.indexOf('>', nextTag);
      state.index = close === -1 ? html.length : close + 1;
      continue;
    }
    const closing = after === '/';
    const close = findTagEnd(html, nextTag);
    const end = close === -1 ? html.length : close + 1;
    const startAt = nextTag + (closing ? 2 : 1);
    const inner = html.slice(startAt, end === html.length ? html.length : end - 1);
    state.index = end;
    return parseTag(inner, closing);
  }
  return null;
}

function currentRawTextContext(stack: StackEntry[]): string | null {
  for (let i = stack.length - 1; i >= 0; i -= 1) {
    const entry = stack[i];
    const isHtmlRaw = entry.namespace === HTML_NS &&
      HTML_RAW_TEXT_ELEMENTS.has(entry.element.localName);
    const isForeignRaw = entry.namespace !== HTML_NS &&
      (entry.element.localName === 'script' || entry.element.localName === 'style');
    if (isHtmlRaw || isForeignRaw) {
      return entry.element.nodeName;
    }
  }
  return null;
}

function rawTextToken(data: string): MemoryNode {
  const node = new MemoryNode(TEXT_NODE, '#text');
  node.textContent = data;
  return node;
}

function resolveNamespace(
  tagName: string,
  active: string,
  stack: StackEntry[],
): string {
  if (tagName === 'svg') {
    return SVG_NS;
  }
  if (tagName === 'math') {
    return MATHML_NS;
  }
  if (active === HTML_NS) {
    return HTML_NS;
  }
  const top = stack[stack.length - 1];
  if (top && HTML_INTEGRATION_POINTS.has(top.element.localName)) {
    return HTML_NS;
  }
  if (active === MATHML_NS && MATHML_TEXT_INTEGRATION.has(tagName)) {
    return MATHML_NS;
  }
  if (BREAKOUT_TAGS.has(tagName)) {
    return HTML_NS;
  }
  return active;
}

export function parseHTML(html: string): MiniNode {
  const fragment = new MemoryFragment();
  const stack: StackEntry[] = [];
  const tokenizerState: TokenizerState = { html, index: 0 };

  const current = (): StackEntry | undefined => stack[stack.length - 1];

  const fosterInsert = (node: MiniNode, boundary: StackEntry): void => {
    let boundaryIndex = stack.lastIndexOf(boundary);
    while (boundaryIndex > 0 && stack[boundaryIndex].element.localName !== 'table') {
      boundaryIndex -= 1;
    }
    const tableNode = stack[boundaryIndex].element;
    const parent = (tableNode.parentNode as MemoryNode) ?? fragment;
    const position = parent.childNodes.indexOf(tableNode);
    node.parentNode = parent;
    if (position === -1) {
      parent.childNodes.push(node);
    } else {
      parent.childNodes.splice(position, 0, node);
    }
  };

  const insert = (node: MiniNode): void => {
    const top = current();
    const isWhitespaceText = node.nodeType === TEXT_NODE &&
      node.textContent.trim().length === 0;
    const isTableContent = node.nodeType === ELEMENT_NODE &&
      TABLE_CONTENT_TAGS.has((node as MemoryElement).localName);
    const inFosterScope = top !== undefined &&
      FOSTER_SCOPE_TAGS.has(top.element.localName) && top.inTable;
    if (inFosterScope && !isTableContent && !isWhitespaceText) {
      fosterInsert(node, top);
      return;
    }
    node.parentNode = top ? top.element : fragment;
    if (top) {
      top.element.childNodes.push(node);
    } else {
      fragment.childNodes.push(node);
    }
  };

  let token = nextToken(tokenizerState, stack);
  while (token !== null) {
    if (!(token instanceof MemoryElement) && token instanceof MemoryNode) {
      if (token.nodeType === COMMENT_NODE) {
        insert(token);
        token = nextToken(tokenizerState, stack);
        continue;
      }
      const top = current();
      if (token.nodeType === TEXT_NODE &&
          token.textContent.trim().length === 0 &&
          !(top && top.namespace === HTML_NS &&
            HTML_RAW_TEXT_ELEMENTS.has(top.element.localName))) {
        token = nextToken(tokenizerState, stack);
        continue;
      }
      if (top && top.namespace === HTML_NS &&
          HTML_RAW_TEXT_ELEMENTS.has(top.element.localName)) {
        top.element.append(token);
      } else {
        insert(token);
      }
      token = nextToken(tokenizerState, stack);
      continue;
    }

    const tag = token as TagToken;
    if (tag.closing) {
      for (let i = stack.length - 1; i >= 0; i -= 1) {
        if (stack[i].element.localName === tag.tagName ||
            stack[i].element.nodeName === tag.tagName) {
          stack.length = i;
          break;
        }
      }
      token = nextToken(tokenizerState, stack);
      continue;
    }

    const active = current()?.namespace ?? HTML_NS;
    const namespace = resolveNamespace(tag.tagName, active, stack);
    const element = new MemoryElement(namespace, tag.tagName);
    for (const [name, value] of tag.attributes) {
      element.setAttribute(name, value);
    }
    insert(element);

    const isVoid = namespace === HTML_NS && HTML_VOID_ELEMENTS.has(tag.tagName);
    if (!isVoid && !tag.selfClosing) {
      const parent = current();
      stack.push({
        element,
        namespace,
        inTable: FOSTER_SCOPE_TAGS.has(tag.tagName) ||
          (parent?.inTable ?? false) && FOSTER_SCOPE_TAGS.has(parent.element.localName),
      });
    }
    token = nextToken(tokenizerState, stack);
  }
  return fragment;
}

function escapeText(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

function escapeAttribute(value: string): string {
  return escapeText(value).replace(/"/g, '&quot;');
}

function serializeNode(node: MiniNode): string {
  if (node.nodeType === TEXT_NODE) {
    const parent = node.parentNode as MemoryElement | null;
    if (parent && parent.namespace === HTML_NS &&
        HTML_RAW_TEXT_ELEMENTS.has(parent.localName)) {
      return node.textContent;
    }
    return escapeText(node.textContent);
  }
  if (node.nodeType !== ELEMENT_NODE) {
    return '';
  }
  const element = node as MemoryElement;
  const name = element.namespaceURI === HTML_NS ? element.localName : element.nodeName;
  let output = `<${name}`;
  for (const attributeName of element.getAttributeNames()) {
    output += ` ${attributeName}="${escapeAttribute(element.getAttribute(attributeName)!)}"`;
  }
  const childHtml = element.childNodes.map(serializeNode).join('');
  if (element.namespaceURI === HTML_NS &&
      HTML_VOID_ELEMENTS.has(element.localName) &&
      element.childNodes.length === 0) {
    return `${output}>`;
  }
  return `${output}>${childHtml}</${name}>`;
}

export function serialize(fragment: MiniNode): string {
  return fragment.childNodes.map(serializeNode).join('');
}

export function createTestDomHost(): DomHost {
  return {
    createFragment(): MiniNode {
      return new MemoryFragment();
    },
    createElement(tag: string): MiniElement {
      return new MemoryElement(HTML_NS, tag);
    },
    createTextNode(data: string): MiniNode {
      const node = new MemoryNode(TEXT_NODE, '#text');
      node.textContent = data;
      return node;
    },
    isElement(node: MiniNode): node is MiniElement {
      return node.nodeType === ELEMENT_NODE;
    },
    isText(node: MiniNode): boolean {
      return node.nodeType === TEXT_NODE;
    },
    appendChild(parent: MiniNode, child: MiniNode): void {
      (parent as MemoryNode).append(child);
    },
    removeChild(parent: MiniNode, child: MiniNode): void {
      const index = parent.childNodes.indexOf(child);
      if (index !== -1) {
        parent.childNodes.splice(index, 1);
        child.parentNode = null;
      }
    },
    clone(node: MiniNode): MiniNode {
      return node.cloneNode(true);
    },
    parseHTML,
    serialize,
  };
}
