/**
 * LL(1) grammar for the expression language:
 *
 *   S         -> Expr EOF
 *   Expr      -> Term ExprRest
 *   ExprRest  -> PLUS Term ExprRest | MINUS Term ExprRest | eps
 *   Term      -> Atom TermRest
 *   TermRest  -> STAR Atom TermRest | SLASH Atom TermRest | eps
 *   Atom      -> INT | IDENT CallSuffix | LPAREN Expr RPAREN | LBRACKET Elems RBRACKET
 *   CallSuffix-> LPAREN Args RPAREN | eps
 *   Args      -> Expr ArgsRest | eps
 *   ArgsRest  -> COMMA Expr ArgsRest | eps
 *   Elems     -> Expr ElemsRest | eps
 *   ElemsRest -> COMMA Expr ElemsRest | eps
 *
 * The grammar is LL(1); the table below is used both by the validator and by
 * the repair search (as the automaton whose configurations are explored).
 */

import type { Token, TokenType } from "./tokens.ts";

export type NonTerminal =
  | "S" | "Expr" | "ExprRest" | "Term" | "TermRest" | "Atom"
  | "CallSuffix" | "Args" | "ArgsRest" | "Elems" | "ElemsRest";

export type GrammarSymbol = NonTerminal | TokenType;

const NON_TERMINALS: ReadonlySet<string> = new Set([
  "S", "Expr", "ExprRest", "Term", "TermRest", "Atom",
  "CallSuffix", "Args", "ArgsRest", "Elems", "ElemsRest",
]);

export function isNonTerminal(sym: GrammarSymbol): sym is NonTerminal {
  return NON_TERMINALS.has(sym);
}

const EXPR_FIRST: readonly TokenType[] = ["INT", "IDENT", "LPAREN", "LBRACKET"];
const EXPR_FOLLOW: readonly TokenType[] = ["EOF", "COMMA", "RPAREN", "RBRACKET"];
const TERM_FOLLOW: readonly TokenType[] = ["PLUS", "MINUS", "EOF", "COMMA", "RPAREN", "RBRACKET"];
const ATOM_FOLLOW: readonly TokenType[] = ["STAR", "SLASH", "PLUS", "MINUS", "EOF", "COMMA", "RPAREN", "RBRACKET"];

function buildTable(): Record<NonTerminal, Partial<Record<TokenType, GrammarSymbol[]>>> {
  const table = {} as Record<NonTerminal, Partial<Record<TokenType, GrammarSymbol[]>>>;
  const add = (nt: NonTerminal, lookaheads: readonly TokenType[], rhs: GrammarSymbol[]): void => {
    const row = (table[nt] ??= {});
    for (const la of lookaheads) {
      if (row[la] !== undefined) throw new Error(`grammar is not LL(1): ${nt} on ${la}`);
      row[la] = rhs;
    }
  };

  add("S", EXPR_FIRST, ["Expr", "EOF"]);
  add("Expr", EXPR_FIRST, ["Term", "ExprRest"]);
  add("ExprRest", ["PLUS"], ["PLUS", "Term", "ExprRest"]);
  add("ExprRest", ["MINUS"], ["MINUS", "Term", "ExprRest"]);
  add("ExprRest", EXPR_FOLLOW, []);
  add("Term", EXPR_FIRST, ["Atom", "TermRest"]);
  add("TermRest", ["STAR"], ["STAR", "Atom", "TermRest"]);
  add("TermRest", ["SLASH"], ["SLASH", "Atom", "TermRest"]);
  add("TermRest", TERM_FOLLOW, []);
  add("Atom", ["INT"], ["INT"]);
  add("Atom", ["IDENT"], ["IDENT", "CallSuffix"]);
  add("Atom", ["LPAREN"], ["LPAREN", "Expr", "RPAREN"]);
  add("Atom", ["LBRACKET"], ["LBRACKET", "Elems", "RBRACKET"]);
  add("CallSuffix", ["LPAREN"], ["LPAREN", "Args", "RPAREN"]);
  add("CallSuffix", ATOM_FOLLOW, []);
  add("Args", EXPR_FIRST, ["Expr", "ArgsRest"]);
  add("Args", ["RPAREN"], []);
  add("ArgsRest", ["COMMA"], ["COMMA", "Expr", "ArgsRest"]);
  add("ArgsRest", ["RPAREN"], []);
  add("Elems", EXPR_FIRST, ["Expr", "ElemsRest"]);
  add("Elems", ["RBRACKET"], []);
  add("ElemsRest", ["COMMA"], ["COMMA", "Expr", "ElemsRest"]);
  add("ElemsRest", ["RBRACKET"], []);
  return table;
}

export const TABLE = buildTable();

/**
 * Expands non-terminals on top of `stack` using `lookahead`, LL(1)-style.
 * Stack is an array with the top at the END.
 * Returns a new stack whose top is a terminal (or which is empty),
 * or null if the parse table has no entry (syntax error configuration).
 */
export function reduceStack(stack: readonly GrammarSymbol[], lookahead: TokenType): GrammarSymbol[] | null {
  const out = stack.slice();
  for (;;) {
    if (out.length === 0) return out;
    const top = out[out.length - 1];
    if (!isNonTerminal(top)) return out;
    const production = TABLE[top][lookahead];
    if (production === undefined) return null;
    out.pop();
    for (let k = production.length - 1; k >= 0; k--) out.push(production[k]);
  }
}

/** Returns true iff the token-type stream is a complete valid program. */
export function parses(types: readonly TokenType[]): boolean {
  let stack: GrammarSymbol[] = ["S"];
  for (const lookahead of [...types, "EOF" as TokenType]) {
    const reduced = reduceStack(stack, lookahead);
    if (reduced === null) return false;
    if (lookahead === "EOF") return reduced.length === 1 && reduced[0] === "EOF";
    if (reduced[reduced.length - 1] !== lookahead) return false;
    reduced.pop();
    stack = reduced;
  }
  return false;
}

export type Ast =
  | { kind: "int"; value: string }
  | { kind: "ident"; name: string }
  | { kind: "call"; name: string; args: Ast[] }
  | { kind: "array"; elems: Ast[] }
  | { kind: "bin"; op: "+" | "-" | "*" | "/"; left: Ast; right: Ast };

/** Recursive-descent parser producing an AST; returns null on syntax error. */
export function parseAst(tokens: readonly Token[]): Ast | null {
  let pos = 0;
  const peek = (): TokenType => (pos < tokens.length ? tokens[pos].type : "EOF");
  const eat = (type: TokenType): Token | null => (peek() === type ? tokens[pos++] : null);

  const parseExpr = (): Ast | null => {
    let left = parseTerm();
    if (left === null) return null;
    while (peek() === "PLUS" || peek() === "MINUS") {
      const op = tokens[pos++].text as "+" | "-";
      const right = parseTerm();
      if (right === null) return null;
      left = { kind: "bin", op, left, right };
    }
    return left;
  };
  const parseTerm = (): Ast | null => {
    let left = parseAtom();
    if (left === null) return null;
    while (peek() === "STAR" || peek() === "SLASH") {
      const op = tokens[pos++].text as "*" | "/";
      const right = parseAtom();
      if (right === null) return null;
      left = { kind: "bin", op, left, right };
    }
    return left;
  };
  const parseList = (close: TokenType): Ast[] | null => {
    const items: Ast[] = [];
    if (peek() !== close) {
      for (;;) {
        const item = parseExpr();
        if (item === null) return null;
        items.push(item);
        if (peek() !== "COMMA") break;
        pos++;
      }
    }
    return eat(close) === null ? null : items;
  };
  const parseAtom = (): Ast | null => {
    const intTok = eat("INT");
    if (intTok !== null) return { kind: "int", value: intTok.text };
    const identTok = eat("IDENT");
    if (identTok !== null) {
      if (eat("LPAREN") !== null) {
        const args = parseList("RPAREN");
        return args === null ? null : { kind: "call", name: identTok.text, args };
      }
      return { kind: "ident", name: identTok.text };
    }
    if (eat("LPAREN") !== null) {
      const inner = parseExpr();
      return inner !== null && eat("RPAREN") !== null ? inner : null;
    }
    if (eat("LBRACKET") !== null) {
      const elems = parseList("RBRACKET");
      return elems === null ? null : { kind: "array", elems };
    }
    return null;
  };

  const result = parseExpr();
  return result !== null && pos === tokens.length ? result : null;
}
