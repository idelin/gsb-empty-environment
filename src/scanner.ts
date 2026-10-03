/**
 * 面向原始 JSON 文本的轻量扫描器。
 *
 * 关键能力：skipValue() 可以在“不物化任何 JS 值”的前提下，
 * 以 O(子串长度) 的纯指针扫描跳过一个无关子树（字符串感知，支持转义）。
 */

export class JsonSyntaxError extends Error {
  constructor(message: string, readonly offset: number) {
    super(`${message} (at offset ${offset})`);
    this.name = 'JsonSyntaxError';
  }
}

const SCALAR_DELIMS = new Set([',', '}', ']', ' ', '\t', '\n', '\r']);

export class JsonScanner {
  pos = 0;

  constructor(readonly text: string) {}

  get eof(): boolean {
    return this.pos >= this.text.length;
  }

  error(message: string, offset = this.pos): JsonSyntaxError {
    return new JsonSyntaxError(message, offset);
  }

  skipWs(): void {
    const text = this.text;
    while (this.pos < text.length) {
      const c = text[this.pos];
      if (c === ' ' || c === '\t' || c === '\n' || c === '\r') this.pos++;
      else break;
    }
  }

  peek(): string {
    this.skipWs();
    return this.text[this.pos];
  }

  expect(ch: string): void {
    this.skipWs();
    if (this.text[this.pos] !== ch) {
      throw this.error(`Expected '${ch}' but found '${this.text[this.pos] ?? '<eof>'}'`);
    }
    this.pos++;
  }

  /** 返回以 start 处引号开头的字符串结束之后的位置（不含任何物化）。 */
  private scanStringEnd(start: number): number {
    const text = this.text;
    let i = start + 1;
    while (i < text.length) {
      const c = text[i];
      if (c === '\\') i += 2;
      else if (c === '"') return i + 1;
      else i++;
    }
    throw this.error('Unterminated string', start);
  }

  skipString(): void {
    if (this.text[this.pos] !== '"') throw this.error('Expected string');
    this.pos = this.scanStringEnd(this.pos);
  }

  /**
   * 跳过任意 JSON 值，返回其起始偏移；调用后 pos 位于该值结束之后。
   * 不构造任何 JS 对象，仅移动指针。
   */
  skipValue(): number {
    this.skipWs();
    const start = this.pos;
    const text = this.text;
    const c = text[this.pos];
    if (c === '"') {
      this.pos = this.scanStringEnd(this.pos);
      return start;
    }
    if (c === '{' || c === '[') {
      let depth = 0;
      let i = this.pos;
      while (i < text.length) {
        const ch = text[i];
        if (ch === '"') {
          i = this.scanStringEnd(i);
          continue;
        }
        if (ch === '{' || ch === '[') depth++;
        else if (ch === '}' || ch === ']') {
          depth--;
          if (depth === 0) {
            i++;
            this.pos = i;
            return start;
          }
        }
        i++;
      }
      throw this.error('Unterminated JSON value', start);
    }
    let i = this.pos;
    while (i < text.length && !SCALAR_DELIMS.has(text[i])) i++;
    if (i === this.pos) throw this.error('Unexpected character', i);
    this.pos = i;
    return start;
  }

  /** 解析当前位置的字符串（处理转义），物化为 JS string。 */
  readString(): string {
    const text = this.text;
    if (text[this.pos] !== '"') throw this.error('Expected string');
    let i = this.pos + 1;
    let chunkStart = i;
    let out = '';
    while (i < text.length) {
      const c = text[i];
      if (c === '"') {
        out += text.slice(chunkStart, i);
        this.pos = i + 1;
        return out;
      }
      if (c === '\\') {
        out += text.slice(chunkStart, i);
        const esc = text[i + 1];
        switch (esc) {
          case '"': out += '"'; break;
          case '\\': out += '\\'; break;
          case '/': out += '/'; break;
          case 'b': out += '\b'; break;
          case 'f': out += '\f'; break;
          case 'n': out += '\n'; break;
          case 'r': out += '\r'; break;
          case 't': out += '\t'; break;
          case 'u': {
            const hex = text.substr(i + 2, 4);
            if (!/^[0-9a-fA-F]{4}$/.test(hex)) throw this.error('Invalid \\u escape', i);
            out += String.fromCharCode(parseInt(hex, 16));
            i += 6;
            chunkStart = i;
            continue;
          }
          default:
            throw this.error(`Invalid escape '\\${esc}'`, i);
        }
        i += 2;
        chunkStart = i;
        continue;
      }
      i++;
    }
    throw this.error('Unterminated string', this.pos);
  }

  /** 解析当前位置的标量（string / number / boolean / null），物化为 JS 值。 */
  readScalar(): unknown {
    this.skipWs();
    if (this.text[this.pos] === '"') return this.readString();
    const start = this.pos;
    let i = this.pos;
    while (i < this.text.length && !SCALAR_DELIMS.has(this.text[i])) i++;
    const token = this.text.slice(start, i);
    this.pos = i;
    if (token === 'true') return true;
    if (token === 'false') return false;
    if (token === 'null') return null;
    if (token === '') throw this.error('Expected value', start);
    const n = Number(token);
    if (Number.isNaN(n)) throw this.error(`Invalid scalar '${token}'`, start);
    return n;
  }
}
