/** A position inside a template source file. Lines and columns are 1-based. */
export interface Pos {
  offset: number;
  line: number;
  col: number;
}

/** A source range, tagged with the template it came from (provenance). */
export interface Span {
  template: string;
  start: Pos;
  end: Pos;
}

export type Node = TextNode | VarNode | IfNode | EachNode | IncludeNode;

export interface TextNode {
  kind: 'text';
  text: string;
  span: Span;
}

/** `{{ user.name }}` — interpolate a dotted path. */
export interface VarNode {
  kind: 'var';
  path: string[];
  span: Span;
}

/** `{{#if path}} ... {{else}} ... {{/if}}` — only the taken branch is evaluated. */
export interface IfNode {
  kind: 'if';
  path: string[];
  then: Node[];
  else: Node[];
  /** Span of the `{{#if ...}}` open tag (used for condition errors). */
  span: Span;
}

/** `{{#each path as item}} ... {{/each}}` — `item` and `@index` shadow outer scopes. */
export interface EachNode {
  kind: 'each';
  path: string[];
  itemName: string;
  body: Node[];
  /** Span of the `{{#each ...}}` open tag. */
  span: Span;
}

/** `{{> partialName}}` — render another template; pushes a call-stack frame. */
export interface IncludeNode {
  kind: 'include';
  name: string;
  span: Span;
}
