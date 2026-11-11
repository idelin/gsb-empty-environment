import { PatternSyntaxError } from './types.ts';
import type {
  AltNode,
  ConcatNode,
  LiteralNode,
  ParsedPattern,
  RegexNode,
  StarNode,
} from './types.ts';

/**
 * 支持语法子集的递归下降解析器：
 *
 *   pattern   := '^'? alternation '$'?
 *   alternation := concatenation ('|' concatenation)*
 *   concatenation := quantified*
 *   quantified  := atom '*'*
 *   atom      := '(' alternation ')' | literal
 *   literal   := 非元字符 | '\\' 任意字符
 *
 * 不支持的构造（字符类、+、?、{n,m}、反向引用、断言等）一律显式报错，
 * 而不是静默按字面量处理，避免给审查宿主错误结论。
 */

const METACHARACTERS = new Set(['|', '(', ')', '*', '^', '$']);
const UNSUPPORTED_METACHARACTERS = new Set(['[', ']', '+', '?', '{', '}', '.', '\\d']);

class Parser {
  private pos = 0;
  private readonly text: string;

  constructor(text: string) {
    this.text = text;
  }

  parse(): ParsedPattern {
    let anchoredStart = false;
    let anchoredEnd = false;

    if (this.peek() === '^') {
      anchoredStart = true;
      this.advance();
    }

    const bodyStart = this.pos;
    const ast = this.parseAlternation();
    const bodyEnd = this.pos;

    if (this.peek() === '$') {
      anchoredEnd = true;
      this.advance();
    }

    if (!this.atEnd()) {
      throw new PatternSyntaxError(
        `锚点 $ 之后存在多余字符 '${this.peek()}'，整串锚点只允许出现在模式末尾`,
        this.pos,
      );
    }

    return {
      ast,
      anchoredStart,
      anchoredEnd,
      body: this.text.slice(bodyStart, bodyEnd),
      source: this.text,
    };
  }

  private parseAlternation(): RegexNode {
    const start = this.pos;
    const options: RegexNode[] = [this.parseConcatenation()];

    while (this.peek() === '|') {
      this.advance();
      options.push(this.parseConcatenation());
    }

    if (options.length === 1) {
      return options[0];
    }

    const node: AltNode = {
      type: 'alt',
      options,
      span: { start, end: this.pos },
    };
    return node;
  }

  private parseConcatenation(): RegexNode {
    const start = this.pos;
    const parts: RegexNode[] = [];

    while (this.canStartAtom()) {
      parts.push(this.parseQuantified());
    }

    if (parts.length === 0) {
      throw new PatternSyntaxError('并集分支或分组为空，期望一个可匹配的原子', this.pos);
    }

    if (parts.length === 1) {
      return parts[0];
    }

    const node: ConcatNode = {
      type: 'concat',
      parts,
      span: { start, end: this.pos },
    };
    return node;
  }

  private parseQuantified(): RegexNode {
    let atom = this.parseAtom();

    while (this.peek() === '*') {
      const starStart = atom.span.start;
      this.advance();
      const star: StarNode = {
        type: 'star',
        child: atom,
        span: { start: starStart, end: this.pos },
      };
      atom = star;
    }

    return atom;
  }

  private parseAtom(): RegexNode {
    const ch = this.peek();

    if (ch === '(') {
      const start = this.pos;
      this.advance();
      const child = this.parseAlternation();
      if (this.peek() !== ')') {
        throw new PatternSyntaxError("分组缺少闭合括号 ')'", this.pos);
      }
      this.advance();
      return {
        type: 'group',
        capture: true,
        child,
        span: { start, end: this.pos },
      };
    }

    return this.parseLiteral();
  }

  private parseLiteral(): LiteralNode {
    const start = this.pos;
    const ch = this.peek();

    if (ch === undefined) {
      throw new PatternSyntaxError('模式意外结束，期望字面量', this.pos);
    }

    if (ch === '\\') {
      this.advance();
      const escaped = this.peek();
      if (escaped === undefined) {
        throw new PatternSyntaxError('转义符后缺少字符', this.pos);
      }
      if (isDigit(escaped)) {
        throw new PatternSyntaxError(
          `反向引用或数字转义 '\\${escaped}' 不受支持；本工具的输入约定为不含反向引用`,
          start,
        );
      }
      this.advance();
      return {
        type: 'literal',
        char: escaped,
        span: { start, end: this.pos },
      };
    }

    if (UNSUPPORTED_METACHARACTERS.has(ch)) {
      throw new PatternSyntaxError(
        `元字符 '${ch}' 不在支持的语法子集内（仅支持字面量、分组、|、*、^、$）`,
        this.pos,
      );
    }

    if (ch === ')' || ch === '|' || ch === '$') {
      throw new PatternSyntaxError(`意外的元字符 '${ch}'`, this.pos);
    }

    if (ch === '*') {
      throw new PatternSyntaxError("量词 '*' 前缺少可重复的原子", this.pos);
    }

    if (METACHARACTERS.has(ch)) {
      throw new PatternSyntaxError(`意外的元字符 '${ch}'`, this.pos);
    }

    this.advance();
    return {
      type: 'literal',
      char: ch,
      span: { start, end: this.pos },
    };
  }

  private canStartAtom(): boolean {
    const ch = this.peek();
    if (ch === undefined) {
      return false;
    }
    return ch !== '|' && ch !== ')' && ch !== '$';
  }

  private peek(): string | undefined {
    return this.text[this.pos];
  }

  private advance(): void {
    this.pos += 1;
  }

  private atEnd(): boolean {
    return this.pos >= this.text.length;
  }
}

function isDigit(ch: string): boolean {
  return ch >= '0' && ch <= '9';
}

/**
 * 解析模式文本，或从 RegExp 实例提取源文本后解析。
 * RegExp 的 flags 不被接受：本工具只做整串匹配分析，标志位会改变语义。
 */
export function parsePattern(input: string | RegExp): ParsedPattern {
  if (input instanceof RegExp) {
    if (input.flags !== '') {
      throw new PatternSyntaxError(`不接受带标志位的 RegExp（收到 flags='${input.flags}'）`, 0);
    }
    input = input.source;
  }

  if (input.length === 0) {
    throw new PatternSyntaxError('空模式不在支持范围内', 0);
  }

  return new Parser(input).parse();
}
