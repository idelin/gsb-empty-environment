export interface NodeBase {
  id: number;
  line: number;
}

export type Stmt =
  | (NodeBase & { kind: 'VarDecl'; declKind: 'let' | 'const'; name: string; init: Expr | null })
  | (NodeBase & { kind: 'ExprStmt'; expr: Expr })
  | (NodeBase & { kind: 'Block'; body: Stmt[] })
  | (NodeBase & { kind: 'If'; test: Expr; consequent: Stmt; alternate: Stmt | null })
  | (NodeBase & { kind: 'While'; test: Expr; body: Stmt })
  | (NodeBase & { kind: 'For'; init: Stmt | null; test: Expr | null; update: Expr | null; body: Stmt })
  | (NodeBase & { kind: 'FunctionDecl'; name: string; params: string[]; body: Stmt[] })
  | (NodeBase & { kind: 'Return'; argument: Expr | null });

export type Expr =
  | (NodeBase & { kind: 'NumberLit'; value: number })
  | (NodeBase & { kind: 'StringLit'; value: string })
  | (NodeBase & { kind: 'BoolLit'; value: boolean })
  | (NodeBase & { kind: 'Identifier'; name: string })
  | (NodeBase & { kind: 'Unary'; op: string; argument: Expr })
  | (NodeBase & { kind: 'Binary'; op: string; left: Expr; right: Expr })
  | (NodeBase & { kind: 'Assign'; name: string; value: Expr })
  | (NodeBase & { kind: 'Call'; callee: Expr; args: Expr[] });

export type AnyNode = Stmt | Expr;

export interface Program {
  body: Stmt[];
}

export function walkStatements(program: Program): Stmt[] {
  const out: Stmt[] = [];
  const visit = (stmt: Stmt): void => {
    out.push(stmt);
    switch (stmt.kind) {
      case 'Block':
        stmt.body.forEach(visit);
        break;
      case 'If':
        visit(stmt.consequent);
        if (stmt.alternate) visit(stmt.alternate);
        break;
      case 'While':
        visit(stmt.body);
        break;
      case 'For':
        if (stmt.init) visit(stmt.init);
        visit(stmt.body);
        break;
      case 'FunctionDecl':
        stmt.body.forEach(visit);
        break;
      default:
        break;
    }
  };
  program.body.forEach(visit);
  return out;
}
