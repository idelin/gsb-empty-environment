/**
 * AST nodes for the supported SQL subset.
 *
 * Grammar (simplified):
 *   selectStmt := SELECT selectList FROM tableRef (',' tableRef)* (WHERE expr)?
 *   expr       := orExpr
 *   orExpr     := andExpr (OR andExpr)*
 *   andExpr    := predicate (AND predicate)*
 *   predicate  := (NOT)? EXISTS '(' selectStmt ')'
 *               | primary ((NOT)? IN '(' selectStmt ')' | cmpOp primary)?
 *               | '(' expr ')'
 */

export type Expr =
  | ColumnExpr
  | LiteralExpr
  | BinaryExpr
  | LogicalExpr
  | ExistsExpr
  | InExpr;

export interface ColumnExpr {
  kind: 'column';
  table: string | null;
  name: string;
}

export interface LiteralExpr {
  kind: 'literal';
  value: number | string | null;
}

export interface BinaryExpr {
  kind: 'binary';
  op: string; // =, <>, !=, <, <=, >, >=
  left: Expr;
  right: Expr;
}

export interface LogicalExpr {
  kind: 'and' | 'or';
  items: Expr[];
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
  | { kind: 'star'; table: string | null }
  | { kind: 'expr'; expr: Expr; alias: string | null };

export interface TableRef {
  table: string;
  alias: string;
}

export interface SelectStmt {
  select: SelectItem[];
  from: TableRef[];
  where: Expr | null;
}
