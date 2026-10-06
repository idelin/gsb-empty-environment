/** A half-open character-offset range into the template source. */
export interface Span {
  start: number;
  end: number;
}

export type Expr =
  | { kind: "path"; span: Span; parts: string[] }
  | { kind: "literal"; span: Span; value: string | number | boolean | null };

export type Node =
  | { kind: "text"; span: Span; text: string }
  | { kind: "output"; span: Span; expr: Expr }
  | { kind: "if"; span: Span; branches: IfBranch[]; elseBody: Node[] | null }
  | { kind: "for"; span: Span; varName: string; iterable: Expr; body: Node[] };

export interface IfBranch {
  /** Span of the `{% if %}` / `{% elif %}` tag that opened this branch. */
  span: Span;
  cond: Expr;
  body: Node[];
}
