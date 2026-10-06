export type Ast =
  | { readonly kind: 'lit'; readonly ch: string }
  | { readonly kind: 'concat'; readonly parts: readonly Ast[] }
  | { readonly kind: 'alt'; readonly options: readonly Ast[] }
  | { readonly kind: 'star'; readonly inner: Ast }
  | { readonly kind: 'group'; readonly index: number; readonly inner: Ast };

export type Tree =
  | { readonly kind: 'lit'; readonly start: number; readonly end: number }
  | { readonly kind: 'concat'; readonly children: readonly Tree[]; readonly start: number; readonly end: number }
  | { readonly kind: 'alt'; readonly choice: number; readonly child: Tree; readonly start: number; readonly end: number }
  | { readonly kind: 'star'; readonly iterations: readonly Tree[]; readonly start: number; readonly end: number }
  | { readonly kind: 'group'; readonly index: number; readonly child: Tree; readonly start: number; readonly end: number };

export type Span = readonly [number, number];

export interface ParsedPattern {
  readonly ast: Ast;
  readonly groupCount: number;
}

export interface MatchVector {
  readonly end: number;
  readonly caps: Array<Span | undefined>;
}

export interface SearchMatch {
  readonly start: number;
  readonly end: number;
  readonly match: string;
  readonly groups: ReadonlyArray<string | undefined>;
}

export type VectorCache = Map<Ast, Map<number, MatchVector[]>>;
