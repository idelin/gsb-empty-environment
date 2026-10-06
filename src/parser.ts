import { Ast } from './ast';

/**
 * 递归下降解析器。文法：
 *   alt    := concat ('|' concat)*
 *   concat := repeat*
 *   repeat := atom '*'*
 *   atom   := 字面字符 | '(' alt ')'
 * 除 | ( ) * 之外的所有字符都是字面字符。捕获组按 '(' 出现顺序从 1 编号。
 */
export function parse(pattern: string): { ast: Ast; groupCount: number } {
  const parser = new Parser(pattern);
  const ast = parser.parseAlt();
  if (parser.pos < pattern.length) {
    throw new SyntaxError(`位置 ${parser.pos} 处有多余的 '${pattern[parser.pos]}'`);
  }
  return { ast, groupCount: parser.groupCount };
}

class Parser {
  pos = 0;
  groupCount = 0;
  private nextId = 1;

  constructor(private readonly input: string) {}

  private id(): number {
    return this.nextId++;
  }

  private peek(): string | undefined {
    return this.input[this.pos];
  }

  parseAlt(): Ast {
    const options: Ast[] = [this.parseConcat()];
    while (this.peek() === '|') {
      this.pos++;
      options.push(this.parseConcat());
    }
    if (options.length === 1) return options[0]!;
    return { kind: 'alt', id: this.id(), options };
  }

  private parseConcat(): Ast {
    const parts: Ast[] = [];
    while (this.pos < this.input.length) {
      const ch = this.peek()!;
      if (ch === '|' || ch === ')') break;
      parts.push(this.parseRepeat());
    }
    if (parts.length === 1) return parts[0]!;
    return { kind: 'concat', id: this.id(), parts }; // 空 parts 即 epsilon
  }

  private parseRepeat(): Ast {
    let node = this.parseAtom();
    while (this.peek() === '*') {
      this.pos++;
      node = { kind: 'star', id: this.id(), child: node };
    }
    return node;
  }

  private parseAtom(): Ast {
    const ch = this.peek();
    if (ch === undefined) {
      throw new SyntaxError('模式意外结束');
    }
    if (ch === '(') {
      this.pos++;
      const index = ++this.groupCount;
      const child = this.parseAlt();
      if (this.peek() !== ')') {
        throw new SyntaxError(`位置 ${this.pos} 处缺少 ')'`);
      }
      this.pos++;
      return { kind: 'group', id: this.id(), index, child };
    }
    if (ch === '*') {
      throw new SyntaxError(`位置 ${this.pos} 处 '*' 前面没有可重复的单元`);
    }
    if (ch === ')' || ch === '|') {
      throw new SyntaxError(`位置 ${this.pos} 处有意外的 '${ch}'`);
    }
    this.pos++;
    return { kind: 'char', id: this.id(), ch };
  }
}
