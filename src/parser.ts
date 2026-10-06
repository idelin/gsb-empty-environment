import type { Expr, IfBranch, Node, Span } from "./ast.ts";
import { TemplateSyntaxError } from "./errors.ts";
import { lex, type Token } from "./lexer.ts";
import type { Source } from "./source.ts";

/** Parses a template into an AST. Throws TemplateSyntaxError on bad input. */
export function parse(source: Source): Node[] {
  const parser = new Parser(source, lex(source));
  return parser.parseNodes([]).nodes;
}

const IDENT = /^[A-Za-z_][A-Za-z0-9_]*/;

class Parser {
  private pos = 0;
  private readonly source: Source;
  private readonly tokens: Token[];

  constructor(source: Source, tokens: Token[]) {
    this.source = source;
    this.tokens = tokens;
  }

  atEnd(): boolean {
    return this.pos >= this.tokens.length;
  }

  peek(): Token {
    return this.tokens[this.pos];
  }

  private next(): Token {
    return this.tokens[this.pos++];
  }

  /**
   * Parses nodes until a tag whose first word is in `terminators`.
   * The terminating tag is consumed and returned; returns null at EOF.
   */
  parseNodes(terminators: string[]): { nodes: Node[]; terminator: Token | null } {
    const nodes: Node[] = [];
    while (!this.atEnd()) {
      const token = this.peek();
      if (token.type === "text") {
        this.next();
        nodes.push({ kind: "text", span: token.span, text: token.text });
        continue;
      }
      if (token.type === "expr") {
        this.next();
        const expr = this.parseExpr(token.content, token.contentOffset);
        nodes.push({ kind: "output", span: token.span, expr });
        continue;
      }
      // tag
      const head = firstWord(token.content);
      if (terminators.includes(head)) {
        return { nodes, terminator: this.next() };
      }
      if (head === "if") {
        nodes.push(this.parseIf());
        continue;
      }
      if (head === "for") {
        nodes.push(this.parseFor());
        continue;
      }
      throw new TemplateSyntaxError(`Unknown tag '{% ${token.content.trim()} %}'`, token.span, this.source);
    }
    return { nodes, terminator: null };
  }

  private parseIf(): Node {
    const ifToken = this.next(); // {% if ... %}
    const branches: IfBranch[] = [];
    let elseBody: Node[] | null = null;

    let cond = this.parseExpr(restOfTag(ifToken, "if"), exprOffset(ifToken, "if"));
    let body = this.parseNodes(["elif", "else", "endif"]);
    branches.push({ span: ifToken.span, cond, body: body.nodes });

    let terminator = body.terminator;
    while (terminator !== null && firstWord(terminator.content) === "elif") {
      cond = this.parseExpr(restOfTag(terminator, "elif"), exprOffset(terminator, "elif"));
      body = this.parseNodes(["elif", "else", "endif"]);
      branches.push({ span: terminator.span, cond, body: body.nodes });
      terminator = body.terminator;
    }
    if (terminator !== null && firstWord(terminator.content) === "else") {
      const rest = terminator.content.trim().slice(4).trim();
      if (rest.length > 0) {
        throw new TemplateSyntaxError("Unexpected content after 'else'", terminator.span, this.source);
      }
      const elsePart = this.parseNodes(["endif"]);
      elseBody = elsePart.nodes;
      terminator = elsePart.terminator;
    }
    if (terminator === null) {
      throw new TemplateSyntaxError("Missing {% endif %} for this {% if %}", ifToken.span, this.source);
    }
    return { kind: "if", span: ifToken.span, branches, elseBody };
  }

  private parseFor(): Node {
    const forToken = this.next(); // {% for x in expr %}
    const inner = forToken.content.trim();
    const match = /^for\s+([A-Za-z_][A-Za-z0-9_]*)\s+in\s+/.exec(inner);
    if (!match) {
      throw new TemplateSyntaxError("Expected '{% for <name> in <expression> %}'", forToken.span, this.source);
    }
    const varName = match[1];
    // Offset of the iterable expression inside the whole template.
    const leadIn = inner.indexOf(match[0]);
    const exprStart = forToken.contentOffset + leadIn + match[0].length;
    const exprText = forToken.content.slice(leadIn + match[0].length);
    const iterable = this.parseExpr(exprText, exprStart);

    const body = this.parseNodes(["endfor"]);
    if (body.terminator === null) {
      throw new TemplateSyntaxError("Missing {% endfor %} for this {% for %}", forToken.span, this.source);
    }
    return { kind: "for", span: forToken.span, varName, iterable, body: body.nodes };
  }

  /**
   * Parses an expression: a dotted path (`user.name`) or a literal
   * (string, number, true, false, null). `baseOffset` is the absolute
   * offset in the template where `text` starts.
   */
  parseExpr(text: string, baseOffset: number): Expr {
    const trimmed = text.trim();
    if (trimmed.length === 0) {
      throw new TemplateSyntaxError("Expected an expression", { start: baseOffset, end: baseOffset + text.length }, this.source);
    }
    const lead = text.length - text.trimStart().length;
    const start = baseOffset + lead;
    const span: Span = { start, end: start + trimmed.length };

    const ch = trimmed[0];
    if (ch === '"' || ch === "'") {
      const value = this.parseStringLiteral(trimmed, span);
      return { kind: "literal", span, value };
    }
    if (/^[0-9-]/.test(trimmed)) {
      const num = Number(trimmed);
      if (!Number.isFinite(num)) {
        throw new TemplateSyntaxError(`Invalid number literal '${trimmed}'`, span, this.source);
      }
      return { kind: "literal", span, value: num };
    }
    if (trimmed === "true") return { kind: "literal", span, value: true };
    if (trimmed === "false") return { kind: "literal", span, value: false };
    if (trimmed === "null") return { kind: "literal", span, value: null };

    const parts: string[] = [];
    let rest = trimmed;
    while (rest.length > 0) {
      const m = IDENT.exec(rest);
      if (!m) {
        throw new TemplateSyntaxError(`Unexpected '${rest[0]}' in expression`, span, this.source);
      }
      parts.push(m[0]);
      rest = rest.slice(m[0].length);
      if (rest.startsWith(".")) rest = rest.slice(1);
      else if (rest.length > 0) {
        throw new TemplateSyntaxError(`Unexpected '${rest[0]}' in expression`, span, this.source);
      }
    }
    return { kind: "path", span, parts };
  }

  private parseStringLiteral(text: string, span: Span): string {
    const quote = text[0];
    if (text.length < 2 || text[text.length - 1] !== quote) {
      throw new TemplateSyntaxError("Unterminated string literal", span, this.source);
    }
    const inner = text.slice(1, -1);
    return inner.replace(/\\([\\'"nrt])/g, (_all, esc: string) => {
      switch (esc) {
        case "n": return "\n";
        case "r": return "\r";
        case "t": return "\t";
        default: return esc;
      }
    });
  }
}

function firstWord(content: string): string {
  const m = IDENT.exec(content.trimStart());
  return m ? m[0] : "";
}

/** Tag content with the leading keyword removed. */
function restOfTag(token: Token, keyword: string): string {
  if (token.type !== "tag") throw new Error("internal: expected tag token");
  const trimmed = token.content.trimStart();
  return trimmed.slice(keyword.length);
}

/** Absolute offset where the expression after `keyword` begins. */
function exprOffset(token: Token, keyword: string): number {
  const lead = token.content.length - token.content.trimStart().length;
  return token.contentOffset + lead + keyword.length;
}
