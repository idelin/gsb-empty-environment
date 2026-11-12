export type PerlTokenType =
  | "variable"
  | "word"
  | "number"
  | "string"
  | "backtick"
  | "regex"
  | "input"
  | "op"
  | "punct"
  | "newline"
  | "eof";

export interface PerlToken {
  type: PerlTokenType;
  value: string;
  raw: string;
  line: number;
  column: number;
  quote?: "'" | '"' | "`" | "qw" | "qq" | "qx" | "q";
  flags?: string;
}

const PAIRED_DELIMITERS: Record<string, string> = {
  "(": ")",
  "[": "]",
  "{": "}",
  "<": ">",
};

const MULTI_CHAR_OPERATORS = [
  "||=",
  "//=",
  "&&=",
  "<<=",
  ">>=",
  "**=",
  "...",
  "=~",
  "!~",
  "=>",
  "->",
  "::",
  "..",
  ".=",
  "+=",
  "-=",
  "*=",
  "/=",
  "%=",
  "||",
  "&&",
  "//",
  "==",
  "!=",
  "<=",
  ">=",
  "<<",
  ">>",
  "**",
  "++",
  "--",
];

function isIdentifierStart(char: string): boolean {
  return /[A-Za-z_\u0080-\uffff]/.test(char);
}

function isIdentifierPart(char: string): boolean {
  return /[A-Za-z0-9_\u0080-\uffff:]/.test(char);
}

export class PerlTokenizer {
  private readonly source: string;
  private index = 0;
  private line = 1;
  private column = 1;
  private expectRegex = false;

  constructor(source: string) {
    this.source = source;
  }

  tokenize(): PerlToken[] {
    const tokens: PerlToken[] = [];
    while (this.index < this.source.length) {
      const char = this.source[this.index];
      if (char === " " || char === "\t" || char === "\r" || char === "\f") {
        this.bump(1);
        continue;
      }
      if (char === "\n") {
        tokens.push(this.makeToken("newline", "\n", 1));
        this.bump(1);
        continue;
      }
      if (char === "#") {
        while (this.index < this.source.length && this.source[this.index] !== "\n") {
          this.bump(1);
        }
        continue;
      }
      if (char === "\\" && this.source[this.index + 1] === "\n") {
        this.bump(2);
        continue;
      }
      if (char === "'" || char === '"' || char === "`") {
        tokens.push(this.readSimpleString(char));
        this.expectRegex = false;
        continue;
      }
      if (char === "/" && this.expectRegex) {
        tokens.push(this.readRegex("/", "/"));
        this.expectRegex = false;
        continue;
      }
      if (char === "$" || char === "@" || char === "%" || char === "&") {
        tokens.push(this.readVariable());
        this.expectRegex = false;
        continue;
      }
      if (char === "<" && this.inputAtCurrentPosition()) {
        tokens.push(this.readInput());
        this.expectRegex = false;
        continue;
      }
      const quotedConstruct = this.quotedConstructPrefix();
      if (quotedConstruct) {
        tokens.push(this.readQuotedConstruct(quotedConstruct));
        this.expectRegex = false;
        continue;
      }
      if (char >= "0" && char <= "9") {
        tokens.push(this.readNumber());
        this.expectRegex = false;
        continue;
      }
      if (isIdentifierStart(char)) {
        tokens.push(this.readWord());
        this.expectRegex = false;
        continue;
      }

      const operator = MULTI_CHAR_OPERATORS.find((candidate) =>
        this.source.startsWith(candidate, this.index),
      );
      if (operator) {
        const token = this.makeToken("op", operator, operator.length);
        tokens.push(token);
        this.bump(operator.length);
        if (operator === "=~" || operator === "!~") this.expectRegex = true;
        continue;
      }

      if ("(){}[],;".includes(char)) {
        tokens.push(this.makeToken("punct", char, 1));
        this.bump(1);
        continue;
      }
      if ("+-*/%.!<>?:=|&^~\\".includes(char)) {
        tokens.push(this.makeToken("op", char, 1));
        this.bump(1);
        continue;
      }

      tokens.push(this.makeToken("op", char, 1));
      this.bump(1);
    }
    tokens.push(this.makeToken("eof", "", 0));
    return tokens;
  }

  private makeToken(type: PerlTokenType, value: string, length: number): PerlToken {
    return {
      type,
      value,
      raw: this.source.slice(this.index, this.index + length),
      line: this.line,
      column: this.column,
    };
  }

  private bump(count: number): void {
    for (let offset = 0; offset < count; offset += 1) {
      if (this.source[this.index] === "\n") {
        this.line += 1;
        this.column = 1;
      } else {
        this.column += 1;
      }
      this.index += 1;
    }
  }

  private readSimpleString(delimiter: string): PerlToken {
    const start = this.currentPosition();
    this.bump(1);
    let content = "";
    let closed = false;
    while (this.index < this.source.length) {
      const char = this.source[this.index];
      if (char === "\\" && this.index + 1 < this.source.length) {
        content += char + this.source[this.index + 1];
        this.bump(2);
        continue;
      }
      if (char === delimiter) {
        this.bump(1);
        closed = true;
        break;
      }
      content += char;
      this.bump(1);
    }
    const raw = this.source.slice(start.index, this.index);
    return {
      type: delimiter === "`" ? "backtick" : "string",
      value: content,
      raw,
      line: start.line,
      column: start.column,
      quote:
        delimiter === '"' ? '"' : delimiter === "`" ? "`" : "'",
      ...(closed ? {} : {}),
    };
  }

  private quotedConstructPrefix(): string | undefined {
    const rest = this.source.slice(this.index);
    const match = /^(qq|qx|qw|q|m|qr)(?=\s*[^A-Za-z0-9_\s])/.exec(rest);
    if (!match) return undefined;
    const prefix = match[1];
    let cursor = this.index + prefix.length;
    while (cursor < this.source.length && /\s/.test(this.source[cursor])) cursor += 1;
    const delimiter = this.source[cursor];
    if (!delimiter || isIdentifierPart(delimiter)) return undefined;
    if (prefix === "m" || prefix === "qr") {
      return delimiter === "=" ? undefined : prefix;
    }
    if ("/!|#".includes(delimiter) && !PAIRED_DELIMITERS[delimiter]) {
      return prefix;
    }
    return prefix;
  }

  private readQuotedConstruct(prefix: string): PerlToken {
    const start = this.currentPosition();
    this.bump(prefix.length);
    while (this.index < this.source.length && /\s/.test(this.source[this.index])) {
      if (this.source[this.index] === "\n") break;
      this.bump(1);
    }
    const opening = this.source[this.index];
    const closing = PAIRED_DELIMITERS[opening] ?? opening;
    const isRegex = prefix === "m" || prefix === "qr";
    if (isRegex) {
      const token = this.readDelimited(opening, closing, true);
      let flags = "";
      while (this.index < this.source.length && /[A-Za-z]/.test(this.source[this.index])) {
        flags += this.source[this.index];
        this.bump(1);
      }
      return {
        ...token,
        type: "regex",
        line: start.line,
        column: start.column,
        flags,
      };
    }
    const token = this.readDelimited(opening, closing, false);
    const quote =
      prefix === "qq"
        ? "qq"
        : prefix === "qx"
          ? "qx"
          : prefix === "qw"
            ? "qw"
            : "q";
    return {
      ...token,
      type: prefix === "qx" ? "backtick" : "string",
      line: start.line,
      column: start.column,
      quote,
    };
  }

  private readDelimited(
    opening: string,
    closing: string,
    preserveEscapes: boolean,
  ): PerlToken {
    const start = this.currentPosition();
    this.bump(1);
    let content = "";
    let depth = 1;
    while (this.index < this.source.length) {
      const char = this.source[this.index];
      if (char === "\\" && this.index + 1 < this.source.length) {
        content += preserveEscapes
          ? char + this.source[this.index + 1]
          : char + this.source[this.index + 1];
        this.bump(2);
        continue;
      }
      if (opening !== closing && char === opening) {
        depth += 1;
        content += char;
        this.bump(1);
        continue;
      }
      if (char === closing) {
        depth -= 1;
        this.bump(1);
        if (depth === 0) break;
        content += char;
        continue;
      }
      content += char;
      this.bump(1);
    }
    return {
      type: "string",
      value: content,
      raw: this.source.slice(start.index, this.index),
      line: start.line,
      column: start.column,
    };
  }

  private readRegex(opening: string, closing: string): PerlToken {
    const start = this.currentPosition();
    const token = this.readDelimited(opening, closing, true);
    let flags = "";
    while (this.index < this.source.length && /[A-Za-z]/.test(this.source[this.index])) {
      flags += this.source[this.index];
      this.bump(1);
    }
    return {
      type: "regex",
      value: token.value,
      raw: this.source.slice(start.index, this.index),
      line: start.line,
      column: start.column,
      flags,
    };
  }

  private readVariable(): PerlToken {
    const start = this.currentPosition();
    const sigil = this.source[this.index];
    this.bump(1);

    if (this.source[this.index] === "#") {
      this.bump(1);
      const nameStart = this.index;
      this.readVariableName();
      return {
        type: "variable",
        value: `$#${this.source.slice(nameStart, this.index)}`,
        raw: this.source.slice(start.index, this.index),
        line: start.line,
        column: start.column,
      };
    }
    if (this.source[this.index] === "{") {
      this.bump(1);
      const nameStart = this.index;
      while (this.index < this.source.length && this.source[this.index] !== "}") {
        this.bump(1);
      }
      const name = this.source.slice(nameStart, this.index);
      if (this.source[this.index] === "}") this.bump(1);
      return {
        type: "variable",
        value: sigil + name,
        raw: this.source.slice(start.index, this.index),
        line: start.line,
        column: start.column,
      };
    }
    if (/[0-9]/.test(this.source[this.index] ?? "")) {
      const numberStart = this.index;
      while (this.index < this.source.length && /[0-9]/.test(this.source[this.index])) {
        this.bump(1);
      }
      return {
        type: "variable",
        value: sigil + this.source.slice(numberStart, this.index),
        raw: this.source.slice(start.index, this.index),
        line: start.line,
        column: start.column,
      };
    }
    if (sigil === "$" && "&`'/+".includes(this.source[this.index] ?? "")) {
      const special = this.source[this.index];
      this.bump(1);
      return {
        type: "variable",
        value: `$${special}`,
        raw: this.source.slice(start.index, this.index),
        line: start.line,
        column: start.column,
      };
    }
    if (sigil === "$" && this.source[this.index] === "$") {
      this.bump(1);
      this.readVariableName();
      return {
        type: "variable",
        value: `$$${this.source.slice(start.index + 2, this.index)}`,
        raw: this.source.slice(start.index, this.index),
        line: start.line,
        column: start.column,
      };
    }

    this.readVariableName();
    return {
      type: "variable",
      value: sigil + this.source.slice(start.index + 1, this.index),
      raw: this.source.slice(start.index, this.index),
      line: start.line,
      column: start.column,
    };
  }

  private readVariableName(): void {
    while (this.index < this.source.length) {
      const char = this.source[this.index];
      if (!isIdentifierPart(char)) break;
      this.bump(1);
    }
  }

  private inputAtCurrentPosition(): boolean {
    const remainder = this.source.slice(this.index);
    return (
      remainder.startsWith("<>") ||
      /^<\s*(?:STDIN|\*|\$[A-Za-z_][A-Za-z0-9_]*)\s*>/.test(remainder)
    );
  }

  private readInput(): PerlToken {
    const start = this.currentPosition();
    let cursor = this.index + 1;
    while (cursor < this.source.length && this.source[cursor] !== ">") cursor += 1;
    if (cursor >= this.source.length) {
      return this.makeToken("op", "<", 1);
    }
    const length = cursor - this.index + 1;
    const raw = this.source.slice(this.index, this.index + length);
    this.bump(length);
    return {
      type: "input",
      value: raw,
      raw,
      line: start.line,
      column: start.column,
    };
  }

  private readNumber(): PerlToken {
    const start = this.currentPosition();
    while (this.index < this.source.length && /[0-9._]/.test(this.source[this.index])) {
      this.bump(1);
    }
    return {
      type: "number",
      value: this.source.slice(start.index, this.index),
      raw: this.source.slice(start.index, this.index),
      line: start.line,
      column: start.column,
    };
  }

  private readWord(): PerlToken {
    const start = this.currentPosition();
    while (this.index < this.source.length && isIdentifierPart(this.source[this.index])) {
      this.bump(1);
    }
    return {
      type: "word",
      value: this.source.slice(start.index, this.index),
      raw: this.source.slice(start.index, this.index),
      line: start.line,
      column: start.column,
    };
  }

  private currentPosition(): { index: number; line: number; column: number } {
    return { index: this.index, line: this.line, column: this.column };
  }
}

export function tokenizePerl(source: string): PerlToken[] {
  return new PerlTokenizer(source).tokenize();
}
