/** Literal values allowed inside filter expressions. */
export type Literal = number | string | boolean | undefined;

export type CmpOp = '==' | '!=' | '<' | '<=' | '>' | '>=';

export interface ChildSeg { type: 'child'; key: string }
export interface IndexSeg { type: 'index'; index: number }
export interface WildcardSeg { type: 'wildcard' }
export interface FilterSeg { type: 'filter'; expr: FilterExpr }
export interface DescentSeg { type: 'descent'; key: string }
export type Segment = ChildSeg | IndexSeg | WildcardSeg | FilterSeg | DescentSeg;

export type Operand =
  | { type: 'self' }                          // `@`
  | { type: 'field'; path: string[] }         // `@.a.b`
  | { type: 'literal'; value: Literal };      // 1 | 's' | true | false | undefined

export type FilterExpr =
  | { type: 'or'; items: FilterExpr[] }
  | { type: 'and'; items: FilterExpr[] }
  | { type: 'not'; expr: FilterExpr }
  | { type: 'exists'; path: string[] }                       // exists(@.a.b)
  | { type: 'truthy'; path: string[] }                       // @.a.b
  | { type: 'compare'; op: CmpOp; left: Operand; right: Operand }
  | { type: 'in'; left: Operand; set: Literal[] };           // @.a in [1, 2]

export interface PathAst { segments: Segment[] }
