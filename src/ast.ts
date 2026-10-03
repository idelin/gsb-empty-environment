// SQL AST 定义（仅覆盖解除关联子查询所需的语法子集）

export type Expr =
  | ColumnRef
  | Literal
  | BinaryExpr
  | ExistsExpr
  | InExpr;

export interface ColumnRef {
  kind: 'column';
  table?: string; // 表别名（限定符）
  name: string;
}

export interface Literal {
  kind: 'literal';
  value: number | string | boolean | null;
}

export type BinaryOp =
  | '='
  | '<>'
  | '!='
  | '<'
  | '<='
  | '>'
  | '>='
  | 'AND'
  | 'OR';

export interface BinaryExpr {
  kind: 'binary';
  op: BinaryOp;
  left: Expr;
  right: Expr;
}

export interface ExistsExpr {
  kind: 'exists';
  negated: boolean; // NOT EXISTS
  subquery: SelectStmt;
}

export interface InExpr {
  kind: 'in';
  negated: boolean; // NOT IN
  expr: Expr;
  subquery: SelectStmt;
}

export type SelectItem =
  | { kind: 'star'; table?: string } // * 或 t.*
  | { kind: 'expr'; expr: Expr; alias?: string };

export interface TableRef {
  kind: 'table';
  name: string;
  alias?: string;
}

export type JoinType = 'SEMI' | 'ANTI' | 'INNER' | 'CROSS';

export interface JoinRef {
  kind: 'join';
  joinType: JoinType;
  left: TableSource;
  right: TableSource;
  on?: Expr; // CROSS JOIN 无 ON
}

export type TableSource = TableRef | JoinRef;

export interface SelectStmt {
  kind: 'select';
  columns: SelectItem[];
  from: TableSource[];
  where?: Expr;
}

export function andAll(exprs: Expr[]): Expr | undefined {
  if (exprs.length === 0) return undefined;
  return exprs.reduce<Expr>((acc, e) => ({ kind: 'binary', op: 'AND', left: acc, right: e }));
}

export function splitAnd(expr: Expr): Expr[] {
  if (expr.kind === 'binary' && expr.op === 'AND') {
    return [...splitAnd(expr.left), ...splitAnd(expr.right)];
  }
  return [expr];
}
