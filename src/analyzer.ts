/**
 * Scope analysis: parses the toy language and emits semantic tokens for every
 * identifier occurrence. Declarations introduce bindings into lexical scopes;
 * references resolve to the nearest enclosing visible declaration (no
 * hoisting). Token positions are reported in UTF-16 code units.
 */

import { ScalarText } from './scalar-text.ts';
import { lex } from './lexer.ts';
import type { LexToken } from './lexer.ts';

export const TokenType = {
  VariableDeclaration: 'variable.declaration',
  ParameterDeclaration: 'parameter.declaration',
  FunctionDeclaration: 'function.declaration',
  VariableReference: 'variable.reference',
  ParameterReference: 'parameter.reference',
  FunctionReference: 'function.reference',
  UnresolvedReference: 'unresolved.reference',
} as const;

export type TokenType = (typeof TokenType)[keyof typeof TokenType];

export interface SemanticToken {
  /** UTF-16 code unit offset, inclusive. */
  start: number;
  /** UTF-16 code unit offset, exclusive. */
  end: number;
  type: TokenType;
  /**
   * Identity of the declaration this token binds to, of the form
   * `<name>@<scalarOffset>`. Declarations bind to themselves; unresolved
   * references carry an empty binding.
   */
  binding: string;
}

type BindingKind = 'variable' | 'parameter' | 'function';

interface Binding {
  kind: BindingKind;
  key: string;
}

const DECLARATION_TYPE: Record<BindingKind, TokenType> = {
  variable: TokenType.VariableDeclaration,
  parameter: TokenType.ParameterDeclaration,
  function: TokenType.FunctionDeclaration,
};

const REFERENCE_TYPE: Record<BindingKind, TokenType> = {
  variable: TokenType.VariableReference,
  parameter: TokenType.ParameterReference,
  function: TokenType.FunctionReference,
};

class Scope {
  private readonly declarations = new Map<string, Binding>();
  readonly parent: Scope | null;

  constructor(parent: Scope | null) {
    this.parent = parent;
  }

  declare(name: string, binding: Binding): void {
    // A later declaration in the same scope shadows the earlier one.
    this.declarations.set(name, binding);
  }

  resolve(name: string): Binding | null {
    let scope: Scope | null = this;
    while (scope !== null) {
      const binding = scope.declarations.get(name);
      if (binding !== undefined) return binding;
      scope = scope.parent;
    }
    return null;
  }
}

const BINARY_PRECEDENCE: Record<string, number> = {
  '+': 1,
  '-': 1,
  '*': 2,
  '/': 2,
};

export function analyzeSource(text: ScalarText): SemanticToken[] {
  const tokens = lex(text);
  const out: SemanticToken[] = [];
  let pos = 0;
  let scope = new Scope(null);

  const peek = (): LexToken => tokens[pos];

  const fail = (message: string, tok: LexToken): never => {
    throw new SyntaxError(`${message} at scalar offset ${tok.start}`);
  };

  const expectValue = (value: string): LexToken => {
    const tok = peek();
    if (tok.value !== value) fail(`expected '${value}' but found '${tok.value || tok.kind}'`, tok);
    pos++;
    return tok;
  };

  const expectIdent = (): LexToken => {
    const tok = peek();
    if (tok.kind !== 'ident') fail(`expected identifier but found '${tok.value || tok.kind}'`, tok);
    pos++;
    return tok;
  };

  const emit = (tok: LexToken, type: TokenType, binding: string): void => {
    out.push({
      start: text.scalarOffsetToUtf16(tok.start),
      end: text.scalarOffsetToUtf16(tok.end),
      type,
      binding,
    });
  };

  const bindingKey = (name: string, tok: LexToken): string => `${name}@${tok.start}`;

  const declare = (name: string, kind: BindingKind, tok: LexToken): void => {
    const binding: Binding = { kind, key: bindingKey(name, tok) };
    scope.declare(name, binding);
    emit(tok, DECLARATION_TYPE[kind], binding.key);
  };

  const reference = (tok: LexToken): void => {
    const binding = scope.resolve(tok.value);
    if (binding !== null) {
      emit(tok, REFERENCE_TYPE[binding.kind], binding.key);
    } else {
      emit(tok, TokenType.UnresolvedReference, '');
    }
  };

  const parsePrimary = (): void => {
    const tok = peek();
    if (tok.kind === 'ident') {
      reference(tok);
      pos++;
    } else if (tok.kind === 'number' || tok.kind === 'string') {
      pos++;
    } else if (tok.value === '(') {
      pos++;
      parseExpr();
      expectValue(')');
    } else {
      fail(`expected expression but found '${tok.value || tok.kind}'`, tok);
    }
    // Postfix call: primary "(" args ")".
    while (peek().value === '(') {
      pos++;
      if (peek().value !== ')') {
        parseExpr();
        while (peek().value === ',') {
          pos++;
          parseExpr();
        }
      }
      expectValue(')');
    }
  };

  const parseExpr = (minPrecedence = 0): void => {
    parsePrimary();
    for (;;) {
      const precedence = BINARY_PRECEDENCE[peek().value] ?? -1;
      if (precedence < minPrecedence) return;
      pos++;
      parseExpr(precedence + 1);
    }
  };

  const parseLet = (): void => {
    pos++; // 'let'
    const name = expectIdent();
    expectValue('=');
    // The initializer resolves in the enclosing scope: `let x = x;` refers
    // to an outer `x`, and the new binding is only visible afterwards.
    parseExpr();
    expectValue(';');
    declare(name.value, 'variable', name);
  };

  const parseFn = (): void => {
    pos++; // 'fn'
    const name = expectIdent();
    declare(name.value, 'function', name);
    expectValue('(');
    const outer = scope;
    scope = new Scope(outer);
    if (peek().value !== ')') {
      for (;;) {
        const param = expectIdent();
        declare(param.value, 'parameter', param);
        if (peek().value !== ',') break;
        pos++;
      }
    }
    expectValue(')');
    expectValue('{');
    while (peek().value !== '}') parseStatement();
    expectValue('}');
    scope = outer;
  };

  const parseBlock = (): void => {
    expectValue('{');
    const outer = scope;
    scope = new Scope(outer);
    while (peek().value !== '}') parseStatement();
    expectValue('}');
    scope = outer;
  };

  const parseStatement = (): void => {
    const tok = peek();
    if (tok.kind === 'keyword' && tok.value === 'let') {
      parseLet();
    } else if (tok.kind === 'keyword' && tok.value === 'fn') {
      parseFn();
    } else if (tok.value === '{') {
      parseBlock();
    } else if (tok.kind === 'eof') {
      fail('unexpected end of input', tok);
    } else {
      parseExpr();
      expectValue(';');
    }
  };

  while (peek().kind !== 'eof') {
    parseStatement();
  }

  // `let` declarations are emitted after their initializer is parsed, so
  // restore source order. Ranges never overlap, making the order total.
  out.sort((a, b) => a.start - b.start);
  return out;
}

/** Throws unless tokens are sorted, non-empty and pairwise non-overlapping. */
export function assertValidTokens(tokens: readonly SemanticToken[]): void {
  for (let i = 0; i < tokens.length; i++) {
    const tok = tokens[i];
    if (
      !Number.isInteger(tok.start) ||
      !Number.isInteger(tok.end) ||
      tok.start < 0 ||
      tok.start >= tok.end
    ) {
      throw new Error(`invalid token range [${tok.start}, ${tok.end}) at index ${i}`);
    }
    if (i > 0 && tok.start < tokens[i - 1].end) {
      throw new Error(
        `tokens overlap: [${tokens[i - 1].start}, ${tokens[i - 1].end}) and [${tok.start}, ${tok.end})`,
      );
    }
  }
}
