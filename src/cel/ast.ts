export type Node =
  | IdentNode
  | SelectNode
  | IndexNode
  | CallNode
  | LiteralNode
  | ListNode
  | MapNode
  | UnaryNode
  | BinaryNode
  | LogicalNode
  | ConditionalNode
  | InNode
  | HasNode
  | MacroNode;

export interface IdentNode {
  kind: 'ident';
  name: string;
}

export interface SelectNode {
  kind: 'select';
  operand: Node;
  field: string;
}

export interface IndexNode {
  kind: 'index';
  operand: Node;
  index: Node;
}

export interface CallNode {
  kind: 'call';
  receiver: Node | null;
  name: string;
  args: Node[];
}

export interface LiteralNode {
  kind: 'literal';
  type: 'int' | 'uint' | 'double' | 'string' | 'bytes' | 'bool' | 'null';
  raw: string;
}

export interface ListNode {
  kind: 'list';
  elements: Node[];
}

export interface MapEntry {
  key: Node;
  value: Node;
}

export interface MapNode {
  kind: 'map';
  entries: MapEntry[];
}

export interface UnaryNode {
  kind: 'unary';
  op: '!' | '-' | '+';
  operand: Node;
}

export interface BinaryNode {
  kind: 'binary';
  op:
    | '+'
    | '-'
    | '*'
    | '/'
    | '%'
    | '<'
    | '<='
    | '>'
    | '>='
    | '=='
    | '!=';
  left: Node;
  right: Node;
}

export interface LogicalNode {
  kind: 'logical';
  op: '&&' | '||';
  left: Node;
  right: Node;
}

export interface InNode {
  kind: 'in';
  left: Node;
  right: Node;
}

export interface ConditionalNode {
  kind: 'conditional';
  test: Node;
  consequent: Node;
  alternate: Node;
}

export interface HasNode {
  kind: 'has';
  operand: Node;
}

export type MacroName =
  | 'all'
  | 'exists'
  | 'exists_one'
  | 'filter'
  | 'map'
  | 'has'
  | 'size';

export interface MacroNode {
  kind: 'macro';
  name: MacroName;
  receiver: Node;
  bindVar: string | null;
  predicate: Node | null;
}
