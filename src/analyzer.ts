import { SourceText } from './sourceText';
import { lex, LexemeKind } from './lexer';

export type TokenType = 'declaration' | 'reference' | 'unresolved' | 'property';

/**
 * A semantic token. Positions are UTF-16 code-unit offsets into the source.
 * `decl` is a binding key identifying the declaration this token is bound to
 * (`<name>@<utf16-start>`); declarations carry their own key, references the
 * key of the declaration they resolve to, unresolved/property tokens none.
 */
export interface SemanticToken {
  start: number;
  length: number;
  type: TokenType;
  decl?: string;
}

interface DeclInfo {
  name: string;
  key: string;
}

class Scope {
  readonly bindings = new Map<string, DeclInfo>();
  constructor(readonly parent: Scope | null) {}

  lookup(name: string): DeclInfo | undefined {
    let scope: Scope | null = this;
    while (scope) {
      const found = scope.bindings.get(name);
      if (found) return found;
      scope = scope.parent;
    }
    return undefined;
  }
}

/**
 * Analyze the source and produce the full, sorted, non-overlapping semantic
 * token list. Lexical scoping with shadowing: an identifier resolves to the
 * nearest enclosing scope that declares its name.
 */
export function analyze(source: SourceText): SemanticToken[] {
  const lexemes = lex(source);
  const tokens: SemanticToken[] = [];
  let scope = new Scope(null);

  for (let i = 0; i < lexemes.length; i++) {
    const lx = lexemes[i];

    if (lx.kind === LexemeKind.Punct) {
      if (lx.text === '{') {
        scope = new Scope(scope);
      } else if (lx.text === '}' && scope.parent) {
        scope = scope.parent;
      }
      continue;
    }

    if (lx.kind === LexemeKind.Keyword && (lx.text === 'let' || lx.text === 'const')) {
      const next = lexemes[i + 1];
      if (next && next.kind === LexemeKind.Identifier) {
        const key = `${next.text}@${next.start}`;
        scope.bindings.set(next.text, { name: next.text, key });
        tokens.push({
          start: next.start,
          length: next.end - next.start,
          type: 'declaration',
          decl: key,
        });
        i++;
      }
      continue;
    }

    if (lx.kind === LexemeKind.Identifier) {
      const prev = lexemes[i - 1];
      if (prev && prev.kind === LexemeKind.Punct && prev.text === '.') {
        tokens.push({ start: lx.start, length: lx.end - lx.start, type: 'property' });
        continue;
      }
      const binding = scope.lookup(lx.text);
      if (binding) {
        tokens.push({
          start: lx.start,
          length: lx.end - lx.start,
          type: 'reference',
          decl: binding.key,
        });
      } else {
        tokens.push({ start: lx.start, length: lx.end - lx.start, type: 'unresolved' });
      }
    }
  }

  return tokens;
}

/** Throws unless tokens are sorted by start and pairwise non-overlapping. */
export function assertWellFormed(tokens: SemanticToken[]): void {
  for (let i = 1; i < tokens.length; i++) {
    const prev = tokens[i - 1];
    const cur = tokens[i];
    if (cur.start < prev.start + prev.length) {
      throw new Error(
        `tokens overlap or out of order at index ${i}: ` +
          `[${prev.start}, ${prev.start + prev.length}) vs [${cur.start}, ${cur.start + cur.length})`,
      );
    }
  }
}
