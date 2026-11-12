import { XmlParseError } from "./errors.ts";

/**
 * 命名空间感知的最小 XML 解析器，零依赖。
 *
 * 安全立场：
 * - 完全拒绝 DOCTYPE，因此不存在 DTD 外部实体/参数实体（XXE）攻击面；
 * - 不支持自定义实体，仅支持五个内置实体与数字字符引用；
 * - 解析结果保留元素的命名空间 URI、本地名、原始 qname 与命名空间声明，
 *   XInclude 识别一律按命名空间 URI 而非前缀匹配。
 */

export const XML_NAMESPACE = "http://www.w3.org/XML/1998/namespace";
export const XMLNS_NAMESPACE = "http://www.w3.org/2000/xmlns/";

export interface XmlAttribute {
  /** 限定名，例如 xi:include 或 xml:base */
  readonly qname: string;
  /** 命名空间 URI；无命名空间属性为 null */
  readonly namespaceUri: string | null;
  /** 冒号后的本地名；无冒号时等于 qname */
  readonly localName: string;
  /** 已解码的属性值 */
  readonly value: string;
}

export type XmlNode = XmlElement | XmlText;

export interface XmlElement {
  readonly kind: "element";
  readonly qname: string;
  readonly namespaceUri: string | null;
  readonly localName: string;
  readonly prefix: string | null;
  readonly attributes: XmlAttribute[];
  /** 本元素上声明的命名空间（前缀 -> URI，"" 表示默认命名空间） */
  readonly namespaceDeclarations: Readonly<Record<string, string>>;
  children: XmlNode[];
}

export interface XmlText {
  readonly kind: "text";
  text: string;
}

export interface XmlDocument {
  readonly root: XmlElement;
}

const BUILTIN_ENTITIES: Record<string, string> = {
  amp: "&",
  lt: "<",
  gt: ">",
  quot: '"',
  apos: "'",
};

export function parseXml(source: string): XmlDocument {
  return new XmlParser(source).parseDocument();
}

function isNameStartChar(code: number): boolean {
  return (
    code === 0x3a ||
    code === 0x5f ||
    (code >= 0x41 && code <= 0x5a) ||
    (code >= 0x61 && code <= 0x7a) ||
    (code >= 0xc0 && code <= 0xd6) ||
    (code >= 0xd8 && code <= 0xf6) ||
    (code >= 0xf8 && code <= 0x2ff) ||
    (code >= 0x370 && code <= 0x37d) ||
    (code >= 0x37f && code <= 0x1fff) ||
    (code >= 0x200c && code <= 0x200d) ||
    (code >= 0x2070 && code <= 0x218f) ||
    (code >= 0x2c00 && code <= 0x2fef) ||
    (code >= 0x3001 && code <= 0xd7ff) ||
    (code >= 0xf900 && code <= 0xfdcf) ||
    (code >= 0xfdf0 && code <= 0xfffd) ||
    (code >= 0x10000 && code <= 0xeffff)
  );
}

function isNameChar(code: number): boolean {
  return (
    isNameStartChar(code) ||
    code === 0x2d ||
    code === 0x2e ||
    (code >= 0x30 && code <= 0x39) ||
    code === 0xb7 ||
    (code >= 0x300 && code <= 0x36f) ||
    (code >= 0x203f && code <= 0x2040)
  );
}

function isHexDigit(code: number): boolean {
  return (
    (code >= 0x30 && code <= 0x39) ||
    (code >= 0x41 && code <= 0x46) ||
    (code >= 0x61 && code <= 0x66)
  );
}

function isValidXmlChar(codePoint: number): boolean {
  return (
    codePoint === 0x9 ||
    codePoint === 0xa ||
    codePoint === 0xd ||
    (codePoint >= 0x20 && codePoint <= 0xd7ff) ||
    (codePoint >= 0xe000 && codePoint <= 0xfffd) ||
    (codePoint >= 0x10000 && codePoint <= 0x10ffff)
  );
}

class XmlParser {
  private pos = 0;
  private readonly source: string;

  constructor(source: string) {
    this.source = source;
  }

  parseDocument(): XmlDocument {
    this.skipMisc();
    if (this.startsWith("<?")) {
      this.consumeUntil("?>");
      this.skipMisc();
    }
    if (this.startsWith("<!")) {
      // 拒绝 DOCTYPE 及任何 DTD 标记，消除 XXE 攻击面。
      throw new XmlParseError("不允许 DTD/DOCTYPE 声明", this.pos);
    }
    const root = this.parseElement(new Map());
    this.skipMisc();
    if (this.pos < this.source.length) {
      throw new XmlParseError("文档根元素之后存在非法内容", this.pos);
    }
    return { root };
  }

  private parseElement(
    parentNamespaces: ReadonlyMap<string, string>,
  ): XmlElement {
    this.expect("<");
    const qname = this.parseName();
    const rawAttributes: Array<{ qname: string; value: string }> = [];
    const seenAttributes = new Set<string>();

    let selfClosing = false;
    for (;;) {
      this.skipWhitespace();
      if (this.startsWith("/>")) {
        this.pos += 2;
        selfClosing = true;
        break;
      }
      if (this.startsWith(">")) {
        this.pos += 1;
        break;
      }
      const attrName = this.parseName();
      this.skipWhitespace();
      this.expect("=");
      this.skipWhitespace();
      const attrValue = this.parseAttributeValue();
      if (seenAttributes.has(attrName)) {
        throw new XmlParseError(`属性 ${attrName} 重复`, this.pos);
      }
      seenAttributes.add(attrName);
      rawAttributes.push({ qname: attrName, value: attrValue });
    }

    const namespaces = new Map(parentNamespaces);
    const namespaceDeclarations: Record<string, string> = {};
    for (const attr of rawAttributes) {
      if (attr.qname === "xmlns") {
        namespaces.set("", attr.value);
        namespaceDeclarations[""] = attr.value;
      } else if (attr.qname.startsWith("xmlns:")) {
        const prefix = attr.qname.slice("xmlns:".length);
        if (prefix.length === 0) {
          throw new XmlParseError("非法的命名空间声明", this.pos);
        }
        namespaces.set(prefix, attr.value);
        namespaceDeclarations[prefix] = attr.value;
      }
    }

    const { prefix, localName, namespaceUri } = resolveQName(
      qname,
      namespaces,
    );

    const attributes: XmlAttribute[] = rawAttributes
      .filter(
        (attr) =>
          attr.qname !== "xmlns" && !attr.qname.startsWith("xmlns:"),
      )
      .map((attr) => {
        const resolved = resolveAttributeQName(attr.qname, namespaces);
        return {
          qname: attr.qname,
          namespaceUri: resolved.namespaceUri,
          localName: resolved.localName,
          value: attr.value,
        };
      });

    const children: XmlNode[] = [];
    if (!selfClosing) {
      this.parseChildren(namespaces, qname, children);
    }

    return {
      kind: "element",
      qname,
      namespaceUri,
      localName,
      prefix,
      attributes,
      namespaceDeclarations,
      children,
    };
  }

  private parseChildren(
    namespaces: ReadonlyMap<string, string>,
    parentQName: string,
    children: XmlNode[],
  ): void {
    for (;;) {
      if (this.startsWith("</")) {
        this.pos += 2;
        const closingName = this.parseName();
        this.skipWhitespace();
        this.expect(">");
        if (closingName !== parentQName) {
          throw new XmlParseError(
            `结束标签 ${closingName} 与开始标签 ${parentQName} 不匹配`,
            this.pos,
          );
        }
        return;
      }
      if (this.startsWith("<!--")) {
        this.consumeComment();
      } else if (this.startsWith("<![CDATA[")) {
        this.appendText(children, this.consumeCdata());
      } else if (this.startsWith("<?")) {
        this.consumeUntil("?>");
      } else if (this.startsWith("<")) {
        children.push(this.parseElement(namespaces));
      } else {
        this.appendText(children, this.parseText());
      }
    }
  }

  private appendText(children: XmlNode[], text: string): void {
    if (text.length === 0) {
      return;
    }
    const last = children[children.length - 1];
    if (last && last.kind === "text") {
      last.text += text;
    } else {
      children.push({ kind: "text", text });
    }
  }

  private parseText(): string {
    let result = "";
    let start = this.pos;
    while (this.pos < this.source.length && !this.startsWith("<")) {
      if (this.startsWith("&")) {
        result += this.source.slice(start, this.pos);
        result += this.parseEntityReference();
        start = this.pos;
        continue;
      }
      if (this.startsWith("]]>")) {
        throw new XmlParseError("文本中不允许出现 ]]>", this.pos);
      }
      this.pos += 1;
    }
    result += this.source.slice(start, this.pos);
    return result;
  }

  private parseAttributeValue(): string {
    const quote = this.peek();
    if (quote !== '"' && quote !== "'") {
      throw new XmlParseError("属性值必须使用引号包裹", this.pos);
    }
    this.pos += 1;
    let value = "";
    let start = this.pos;
    while (this.pos < this.source.length && this.peek() !== quote) {
      if (this.startsWith("<")) {
        throw new XmlParseError("属性值中不允许出现 <", this.pos);
      }
      if (this.startsWith("&")) {
        value += this.source.slice(start, this.pos);
        value += this.parseEntityReference();
        start = this.pos;
        continue;
      }
      this.pos += 1;
    }
    if (this.pos >= this.source.length) {
      throw new XmlParseError("属性值缺少结束引号", this.pos);
    }
    value += this.source.slice(start, this.pos);
    this.pos += 1;
    return value;
  }

  private parseEntityReference(): string {
    const entityStart = this.pos;
    this.pos += 1;
    if (this.startsWith("#")) {
      this.pos += 1;
      const hex = this.startsWith("x") || this.startsWith("X");
      if (hex) {
        this.pos += 1;
      }
      const digitsStart = this.pos;
      while (
        this.pos < this.source.length &&
        isHexDigit(this.source.charCodeAt(this.pos))
      ) {
        this.pos += 1;
      }
      const digits = this.source.slice(digitsStart, this.pos);
      if (digits.length === 0 || !this.startsWith(";")) {
        throw new XmlParseError("非法的数字字符引用", entityStart);
      }
      this.pos += 1;
      const codePoint = hex ? parseInt(digits, 16) : parseInt(digits, 10);
      if (!isValidXmlChar(codePoint)) {
        throw new XmlParseError("字符引用不在 XML 合法字符范围内", entityStart);
      }
      return String.fromCodePoint(codePoint);
    }

    const nameStart = this.pos;
    while (
      this.pos < this.source.length &&
      isNameChar(this.source.codePointAt(this.pos) ?? 0)
    ) {
      this.pos += 1;
    }
    const entityName = this.source.slice(nameStart, this.pos);
    if (entityName.length === 0 || !this.startsWith(";")) {
      throw new XmlParseError("非法的实体引用", entityStart);
    }
    this.pos += 1;
    if (!(entityName in BUILTIN_ENTITIES)) {
      throw new XmlParseError(`未定义的实体 &${entityName};`, entityStart);
    }
    return BUILTIN_ENTITIES[entityName];
  }

  private consumeCdata(): string {
    const start = this.pos + "<![CDATA[".length;
    const end = this.source.indexOf("]]>", start);
    if (end === -1) {
      throw new XmlParseError("CDATA 节缺少 ]]>", this.pos);
    }
    const text = this.source.slice(start, end);
    this.pos = end + "]]>".length;
    return text;
  }

  private consumeComment(): void {
    const contentStart = this.pos + "<!--".length;
    const end = this.source.indexOf("-->", contentStart);
    if (end === -1 || this.source.slice(contentStart, end).includes("--")) {
      throw new XmlParseError("注释格式非法", this.pos);
    }
    this.pos = end + "-->".length;
  }

  private consumeUntil(marker: string): void {
    const start = this.pos;
    const end = this.source.indexOf(marker, this.pos);
    if (end === -1) {
      throw new XmlParseError(`缺少结束标记 ${marker}`, start);
    }
    this.pos = end + marker.length;
  }

  private parseName(): string {
    const start = this.pos;
    if (
      this.pos >= this.source.length ||
      !isNameStartChar(this.source.codePointAt(this.pos) ?? 0)
    ) {
      throw new XmlParseError("预期 XML 名称", this.pos);
    }
    this.pos += 1;
    while (
      this.pos < this.source.length &&
      isNameChar(this.source.codePointAt(this.pos) ?? 0)
    ) {
      this.pos += 1;
    }
    return this.source.slice(start, this.pos);
  }

  private skipWhitespace(): void {
    while (
      this.pos < this.source.length &&
      /\s/.test(this.source[this.pos])
    ) {
      this.pos += 1;
    }
  }

  private skipMisc(): void {
    for (;;) {
      this.skipWhitespace();
      if (this.startsWith("<!--")) {
        this.consumeComment();
      } else if (this.startsWith("<?")) {
        this.consumeUntil("?>");
      } else {
        return;
      }
    }
  }

  private expect(literal: string): void {
    if (!this.startsWith(literal)) {
      throw new XmlParseError(`预期 "${literal}"`, this.pos);
    }
    this.pos += literal.length;
  }

  private startsWith(literal: string): boolean {
    return this.source.startsWith(literal, this.pos);
  }

  private peek(): string {
    return this.source[this.pos];
  }
}

function resolveQName(
  qname: string,
  namespaces: ReadonlyMap<string, string>,
): { prefix: string | null; localName: string; namespaceUri: string | null } {
  const colon = qname.indexOf(":");
  if (colon === -1) {
    return {
      prefix: null,
      localName: qname,
      namespaceUri: namespaces.get("") ?? null,
    };
  }
  const prefix = qname.slice(0, colon);
  const localName = qname.slice(colon + 1);
  if (localName.length === 0 || prefix.length === 0) {
    throw new XmlParseError(`非法的限定名 ${qname}`, 0);
  }
  if (prefix === "xml") {
    return { prefix, localName, namespaceUri: XML_NAMESPACE };
  }
  if (prefix === "xmlns") {
    return { prefix, localName, namespaceUri: XMLNS_NAMESPACE };
  }
  const namespaceUri = namespaces.get(prefix) ?? null;
  if (namespaceUri === null) {
    throw new XmlParseError(`前缀 ${prefix}: 未绑定命名空间`, 0);
  }
  return { prefix, localName, namespaceUri };
}

/**
 * 属性名解析遵循 XML 命名空间规范：无前缀属性不在默认命名空间中，
 * 其命名空间 URI 为 null；带前缀属性按前缀绑定解析。
 */
function resolveAttributeQName(
  qname: string,
  namespaces: ReadonlyMap<string, string>,
): { prefix: string | null; localName: string; namespaceUri: string | null } {
  const colon = qname.indexOf(":");
  if (colon === -1) {
    return { prefix: null, localName: qname, namespaceUri: null };
  }
  return resolveQName(qname, namespaces);
}
