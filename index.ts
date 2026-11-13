export interface Attribute {
  name: string;
  value: string;
}

export interface ElementNode {
  type: 'element';
  tagName: string;
  attributes: Attribute[];
  children: Node[];
}

export interface TextNode {
  type: 'text';
  text: string;
}

export type Node = ElementNode | TextNode;

export interface ParsedStartTag {
  tagName: string;
  attributes: Attribute[];
  next: number;
}

const NAMED_REFERENCES: Readonly<Record<string, string>> = {
  lt: '<',
  gt: '>',
  amp: '&',
  quot: '"',
  apos: "'",
};

const REFERENCE_PATTERN = /&(#\d+|#[xX][0-9a-fA-F]+|[a-zA-Z]+);/g;

function decodeReferenceBody(body: string): string | undefined {
  if (body[0] === '#') {
    const hex = body[1] === 'x' || body[1] === 'X';
    const codePoint = Number.parseInt(body.slice(hex ? 2 : 1), hex ? 16 : 10);
    if (!Number.isFinite(codePoint) || codePoint < 0 || codePoint > 0x10ffff) {
      return undefined;
    }
    return String.fromCodePoint(codePoint);
  }
  return NAMED_REFERENCES[body];
}

/**
 * 替换完整到分号的字符引用；缺分号的引用（如 `&lt`）按字面保留。
 * 文本节点与属性值共用同一条裁决规则。
 */
export function decodeReferences(input: string): string {
  return input.replace(REFERENCE_PATTERN, (match, body: string) => {
    return decodeReferenceBody(body) ?? match;
  });
}

function isWhitespace(ch: string): boolean {
  return ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r' || ch === '\f';
}

/**
 * 解析起始标签。带引号的属性值先在原始源码中定位匹配的结束引号，
 * 再对截取出的值做引用替换——字符引用（无论是否完整）都不会
 * 改变引号边界，缺分号的引用不会提前结束属性。
 */
export function parseStartTag(source: string, start = 0): ParsedStartTag | null {
  if (source[start] !== '<') return null;
  let i = start + 1;
  const tagNameMatch = /^[a-zA-Z][^\s/>]*/.exec(source.slice(i));
  if (!tagNameMatch) return null;
  const tagName = tagNameMatch[0];
  i += tagName.length;

  const attributes: Attribute[] = [];
  for (;;) {
    while (i < source.length && isWhitespace(source[i])) i++;
    if (i >= source.length) return null;
    if (source[i] === '>') {
      i++;
      return { tagName, attributes, next: i };
    }
    if (source[i] === '/' && source[i + 1] === '>') {
      i += 2;
      return { tagName, attributes, next: i };
    }
    const nameMatch = /^[^\s=/>"'<]+/.exec(source.slice(i));
    if (!nameMatch) return null;
    const name = nameMatch[0];
    i += name.length;
    while (i < source.length && isWhitespace(source[i])) i++;

    let value = '';
    if (source[i] === '=') {
      i++;
      while (i < source.length && isWhitespace(source[i])) i++;
      const quote = source[i];
      if (quote === '"' || quote === "'") {
        const close = source.indexOf(quote, i + 1);
        if (close === -1) return null;
        value = decodeReferences(source.slice(i + 1, close));
        i = close + 1;
      } else {
        const unquotedMatch = /^[^\s>]*/.exec(source.slice(i));
        const raw = unquotedMatch ? unquotedMatch[0] : '';
        value = decodeReferences(raw);
        i += raw.length;
      }
    }
    attributes.push({ name, value });
  }
}

function parseNodes(
  source: string,
  start: number,
  closingTag: string | undefined,
): { nodes: Node[]; next: number } {
  const nodes: Node[] = [];
  let i = start;
  let text = '';
  const flushText = (): void => {
    if (text.length > 0) {
      nodes.push({ type: 'text', text: decodeReferences(text) });
      text = '';
    }
  };

  while (i < source.length) {
    if (source.startsWith('</', i)) {
      const closeMatch = /^<\/([a-zA-Z][^\s>]*)\s*>/.exec(source.slice(i));
      if (closeMatch) {
        if (closingTag !== undefined && closeMatch[1].toLowerCase() === closingTag) {
          flushText();
          return { nodes, next: i + closeMatch[0].length };
        }
        text += closeMatch[0];
        i += closeMatch[0].length;
        continue;
      }
    }
    if (source[i] === '<' && /[a-zA-Z]/.test(source[i + 1] ?? '')) {
      const tag = parseStartTag(source, i);
      if (tag) {
        flushText();
        const inner = parseNodes(source, tag.next, tag.tagName.toLowerCase());
        nodes.push({
          type: 'element',
          tagName: tag.tagName,
          attributes: tag.attributes,
          children: inner.nodes,
        });
        i = inner.next;
        continue;
      }
    }
    text += source[i];
    i++;
  }
  flushText();
  return { nodes, next: i };
}

/** 解析文档片段：文本节点中的 `&lt;` 等完整引用替换为对应字符。 */
export function parseFragment(source: string): Node[] {
  return parseNodes(source, 0, undefined).nodes;
}
