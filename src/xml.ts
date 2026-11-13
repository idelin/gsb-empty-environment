/**
 * 最小化 XML 解析器，仅覆盖 MPD 所需能力：元素、属性、文本、CDATA、
 * 注释/声明跳过与实体解码。不依赖 DOMParser，因此同构可用于 Node 与浏览器。
 */

export interface XmlNode {
  /** 去掉命名空间前缀后的本地标签名（小写）。 */
  tag: string;
  /** 原始限定名，例如 `cenc:pssh`。 */
  qualifiedName: string;
  attrs: Record<string, string>;
  children: XmlNode[];
  text: string;
}

const ENTITIES: Record<string, string> = {
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
};

function decodeEntities(input: string): string {
  return input.replace(/&(#x?[0-9a-fA-F]+|[a-zA-Z]+);/g, (match, body: string) => {
    if (body[0] === '#') {
      const code = body[1] === 'x' || body[1] === 'X'
        ? parseInt(body.slice(2), 16)
        : parseInt(body.slice(1), 10);
      return Number.isFinite(code) ? String.fromCodePoint(code) : match;
    }
    return ENTITIES[body] ?? match;
  });
}

function localName(qualified: string): string {
  const idx = qualified.indexOf(':');
  return (idx >= 0 ? qualified.slice(idx + 1) : qualified).toLowerCase();
}

export function parseXml(xml: string): XmlNode {
  const root: XmlNode = {
    tag: '#root',
    qualifiedName: '#root',
    attrs: {},
    children: [],
    text: '',
  };
  const stack: XmlNode[] = [root];
  let i = 0;
  const len = xml.length;

  const pushText = (raw: string): void => {
    if (!raw) return;
    const top = stack[stack.length - 1];
    top.text += decodeEntities(raw);
  };

  while (i < len) {
    const lt = xml.indexOf('<', i);
    if (lt < 0) {
      pushText(xml.slice(i));
      break;
    }
    if (lt > i) pushText(xml.slice(i, lt));
    const next = xml[lt + 1];

    if (next === '?') {
      const close = xml.indexOf('?>', lt + 2);
      i = close < 0 ? len : close + 2;
      continue;
    }
    if (next === '!') {
      if (xml.startsWith('<!--', lt)) {
        const close = xml.indexOf('-->', lt + 4);
        i = close < 0 ? len : close + 3;
      } else if (xml.startsWith('<![CDATA[', lt)) {
        const close = xml.indexOf(']]>', lt + 9);
        const end = close < 0 ? len : close;
        stack[stack.length - 1].text += xml.slice(lt + 9, end);
        i = close < 0 ? len : close + 3;
      } else {
        const close = xml.indexOf('>', lt + 2);
        i = close < 0 ? len : close + 1;
      }
      continue;
    }

    const close = xml.indexOf('>', lt + 1);
    if (close < 0) break;
    let tagContent = xml.slice(lt + 1, close);
    const selfClosing = tagContent.endsWith('/');
    if (selfClosing) tagContent = tagContent.slice(0, -1);
    const closing = tagContent.startsWith('/');
    if (closing) tagContent = tagContent.slice(1);

    const spaceIdx = tagContent.search(/\s/);
    const qualifiedName = (spaceIdx < 0 ? tagContent : tagContent.slice(0, spaceIdx)).trim();
    const attrPart = spaceIdx < 0 ? '' : tagContent.slice(spaceIdx + 1);

    if (closing) {
      const target = localName(qualifiedName);
      for (let depth = stack.length - 1; depth > 0; depth--) {
        if (stack[depth].tag === target) {
          stack.length = depth;
          break;
        }
      }
      i = close + 1;
      continue;
    }

    const node: XmlNode = {
      tag: localName(qualifiedName),
      qualifiedName,
      attrs: parseAttributes(attrPart),
      children: [],
      text: '',
    };
    stack[stack.length - 1].children.push(node);
    if (!selfClosing) stack.push(node);
    i = close + 1;
  }

  return root;
}

function parseAttributes(source: string): Record<string, string> {
  const attrs: Record<string, string> = {};
  const re = /([^\s=]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/g;
  let match: RegExpExecArray | null;
  while ((match = re.exec(source)) !== null) {
    const qualified = match[1];
    attrs[localName(qualified)] = decodeEntities(match[2] ?? match[3] ?? '');
    attrs[qualified] = decodeEntities(match[2] ?? match[3] ?? '');
  }
  return attrs;
}

export function findChildren(node: XmlNode, tag: string): XmlNode[] {
  return node.children.filter((child) => child.tag === tag.toLowerCase());
}

export function findDescendants(node: XmlNode, tag: string): XmlNode[] {
  const wanted = tag.toLowerCase();
  const out: XmlNode[] = [];
  const walk = (current: XmlNode): void => {
    for (const child of current.children) {
      if (child.tag === wanted) out.push(child);
      walk(child);
    }
  };
  walk(node);
  return out;
}
