/**
 * 带标签有序树：树自动机的输入。
 * JSON 与 XML 文档都会被归约为该结构后再校验。
 */

export interface TreeNode {
  label: string;
  children: TreeNode[];
  /** 叶子文本内容（JSON 原始值 / XML 字符数据） */
  text?: string;
}

export function node(label: string, children: TreeNode[] = [], text?: string): TreeNode {
  return text === undefined ? { label, children } : { label, children, text };
}

/**
 * JSON → 带标签有序树。
 * - 对象属性成为子节点（保持插入顺序）；
 * - 数组展开为若干同名兄弟节点（如 {paragraph: [...]} → 多个 paragraph）；
 * - 原始值成为叶子文本。
 * 根：若文档为单键对象，则以该键为根标签，否则使用 '$root'。
 */
export function jsonToTree(document: unknown): TreeNode {
  if (isPlainObject(document)) {
    const keys = Object.keys(document);
    if (keys.length === 1) {
      return jsonValueToNode(keys[0], (document as Record<string, unknown>)[keys[0]]);
    }
    return node('$root', jsonObjectToChildren(document as Record<string, unknown>));
  }
  return jsonValueToNode('$root', document);
}

function jsonObjectToChildren(object: Record<string, unknown>): TreeNode[] {
  const children: TreeNode[] = [];
  for (const [key, value] of Object.entries(object)) {
    if (Array.isArray(value)) {
      for (const item of value) {
        children.push(jsonValueToNode(key, item));
      }
    } else {
      children.push(jsonValueToNode(key, value));
    }
  }
  return children;
}

function jsonValueToNode(label: string, value: unknown): TreeNode {
  if (isPlainObject(value)) {
    return node(label, jsonObjectToChildren(value as Record<string, unknown>));
  }
  if (Array.isArray(value)) {
    const children: TreeNode[] = [];
    for (const item of value) {
      children.push(jsonValueToNode(label, item));
    }
    return node(label, children);
  }
  return node(label, [], value === null ? '' : String(value));
}

function isPlainObject(value: unknown): boolean {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * 极简 XML 解析（元素 + 字符数据 + 注释 + 声明，忽略属性），
 * 产出与 JSON 相同的带标签有序树。非法输入抛出 SyntaxError。
 */
export function xmlToTree(source: string): TreeNode {
  const parser = new XmlParser(source);
  return parser.parseDocument();
}

class XmlParser {
  private pos = 0;
  private readonly source: string;

  constructor(source: string) {
    this.source = source;
  }

  parseDocument(): TreeNode {
    this.skipMisc();
    const root = this.parseElement();
    this.skipMisc();
    if (this.pos < this.source.length) {
      this.fail('根元素之后存在多余内容');
    }
    return root;
  }

  private parseElement(): TreeNode {
    this.expect('<');
    if (this.peek() === '/' || this.peek() === '?' || this.peek() === '!') {
      this.fail('此处应为开始标签');
    }
    const label = this.parseName();
    this.skipAttributes();
    if (this.consume('/>')) {
      return node(label);
    }
    this.expect('>');
    const children: TreeNode[] = [];
    const texts: string[] = [];
    for (;;) {
      if (this.pos >= this.source.length) {
        this.fail(`元素 <${label}> 未闭合`);
      }
      if (this.startsWith('</')) {
        this.pos += 2;
        const closing = this.parseName();
        this.expect('>');
        if (closing !== label) {
          this.fail(`标签不匹配：期望 </${label}>，实际 </${closing}>`);
        }
        break;
      }
      if (this.startsWith('<!--')) {
        this.skipComment();
        continue;
      }
      if (this.startsWith('<![CDATA[')) {
        texts.push(this.parseCdata());
        continue;
      }
      if (this.peek() === '<') {
        children.push(this.parseElement());
        continue;
      }
      texts.push(this.parseText());
    }
    const text = texts.join('').trim();
    return text.length > 0 ? node(label, children, text) : node(label, children);
  }

  private parseName(): string {
    const match = /^[A-Za-z_][A-Za-z0-9_.-]*/.exec(this.source.slice(this.pos));
    if (!match) {
      this.fail('期望标签名');
    }
    this.pos += match[0].length;
    return match[0];
  }

  private parseText(): string {
    const end = this.source.indexOf('<', this.pos);
    const stop = end === -1 ? this.source.length : end;
    const text = this.source.slice(this.pos, stop);
    this.pos = stop;
    return decodeEntities(text);
  }

  private parseCdata(): string {
    const end = this.source.indexOf(']]>', this.pos);
    if (end === -1) {
      this.fail('CDATA 未闭合');
    }
    const text = this.source.slice(this.pos + '<![CDATA['.length, end);
    this.pos = end + 3;
    return text;
  }

  private skipAttributes(): void {
    for (;;) {
      this.skipWhitespace();
      const ch = this.peek();
      if (ch === '>' || ch === '/' || ch === undefined) {
        return;
      }
      this.parseName();
      this.skipWhitespace();
      this.expect('=');
      this.skipWhitespace();
      const quote = this.source[this.pos];
      if (quote !== '"' && quote !== "'") {
        this.fail('属性值必须使用引号');
      }
      const end = this.source.indexOf(quote, this.pos + 1);
      if (end === -1) {
        this.fail('属性值未闭合');
      }
      this.pos = end + 1;
    }
  }

  private skipMisc(): void {
    for (;;) {
      this.skipWhitespace();
      if (this.startsWith('<?')) {
        const end = this.source.indexOf('?>', this.pos);
        if (end === -1) this.fail('XML 声明未闭合');
        this.pos = end + 2;
        continue;
      }
      if (this.startsWith('<!--')) {
        this.skipComment();
        continue;
      }
      if (this.startsWith('<!DOCTYPE')) {
        const end = this.source.indexOf('>', this.pos);
        if (end === -1) this.fail('DOCTYPE 未闭合');
        this.pos = end + 1;
        continue;
      }
      return;
    }
  }

  private skipComment(): void {
    const end = this.source.indexOf('-->', this.pos);
    if (end === -1) {
      this.fail('注释未闭合');
    }
    this.pos = end + 3;
  }

  private skipWhitespace(): void {
    while (this.pos < this.source.length && /\s/.test(this.source[this.pos])) {
      this.pos += 1;
    }
  }

  private startsWith(prefix: string): boolean {
    return this.source.startsWith(prefix, this.pos);
  }

  private consume(token: string): boolean {
    if (this.startsWith(token)) {
      this.pos += token.length;
      return true;
    }
    return false;
  }

  private expect(token: string): void {
    if (!this.consume(token)) {
      this.fail(`期望 '${token}'`);
    }
  }

  private peek(): string | undefined {
    return this.source[this.pos];
  }

  private fail(message: string): never {
    throw new SyntaxError(`XML 解析失败（位置 ${this.pos}）：${message}`);
  }
}

function decodeEntities(text: string): string {
  return text
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&apos;/g, "'")
    .replace(/&amp;/g, '&');
}
