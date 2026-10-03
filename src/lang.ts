import { AstNode, at, SourcePos } from './ast';
import {
  AttributeGrammar,
  child,
  Diagnostic,
  parent,
  self,
  sibling,
  SemanticRule,
} from './grammar';

/* ------------------------------------------------------------------ */
/* Types and environments                                              */
/* ------------------------------------------------------------------ */

export type TypeName = 'int' | 'bool' | 'error';

export interface EnvEntry {
  readonly type: TypeName;
  readonly value: unknown;
}

/** Symbol-table environment: variable name -> type + runtime value. */
export type Env = ReadonlyMap<string, EnvEntry>;

export function formatDiagnostic(d: Diagnostic): string {
  return `${d.pos.line}:${d.pos.column} ${d.message}`;
}

/* ------------------------------------------------------------------ */
/* AST builders (the AST is assumed to be produced by a parser)        */
/* ------------------------------------------------------------------ */

export const program = (stmts: AstNode[], pos: SourcePos = at(1, 1)): AstNode => {
  const p = new AstNode('Program', pos);
  stmts.forEach((s) => p.add(s));
  return p;
};

export const varDecl = (
  name: string,
  declaredType: Exclude<TypeName, 'error'>,
  init: AstNode,
  pos: SourcePos,
): AstNode => new AstNode('VarDecl', pos, { name, declaredType }).add(init);

export const exprStmt = (expr: AstNode, pos: SourcePos): AstNode =>
  new AstNode('ExprStmt', pos).add(expr);

export const num = (value: number, pos: SourcePos): AstNode =>
  new AstNode('Num', pos, { value });

export const bool = (value: boolean, pos: SourcePos): AstNode =>
  new AstNode('Bool', pos, { value });

export const varRef = (name: string, pos: SourcePos): AstNode =>
  new AstNode('Var', pos, { name });

export type Op = '+' | '-' | '*' | '<' | '>' | '==' | '&&' | '||';

export const binary = (op: Op, left: AstNode, right: AstNode, pos: SourcePos): AstNode =>
  new AstNode('Binary', pos, { op }).add(left).add(right);

/* ------------------------------------------------------------------ */
/* The attribute grammar                                               */
/*                                                                     */
/* Inherited attributes (top-down):                                    */
/*   env        symbol table visible at this node                      */
/* Synthesized attributes (bottom-up):                                 */
/*   globalEnv  empty root environment (Program)                       */
/*   envOut     environment after this statement (adds declarations)   */
/*   type       inferred expression type                               */
/*   value      evaluated expression result                            */
/*   check      declaration type-correctness (VarDecl)                 */
/* ------------------------------------------------------------------ */

/** Inherited `env` for statements: first statement reads the program's
 *  globalEnv, later statements read the previous sibling's envOut. */
const stmtEnvRule: SemanticRule = {
  attr: 'env',
  kind: 'inherited',
  dependsOn: (node) =>
    node.childIndex === 0 ? [parent('globalEnv')] : [sibling(-1, 'envOut')],
  compute: (node, ctx) =>
    ctx.get<Env>(node.childIndex === 0 ? parent('globalEnv') : sibling(-1, 'envOut')),
};

/** Inherited `env` for expressions: copied down from the parent node. */
const exprEnvRule: SemanticRule = {
  attr: 'env',
  kind: 'inherited',
  dependsOn: () => [parent('env')],
  compute: (_node, ctx) => ctx.get<Env>(parent('env')),
};

interface OpSpec {
  readonly args: readonly [TypeName, TypeName] | 'same';
  readonly result: TypeName;
  apply(a: never, b: never): unknown;
}

const OPS: Record<Op, OpSpec> = {
  '+': { args: ['int', 'int'], result: 'int', apply: (a: number, b: number) => a + b } as OpSpec,
  '-': { args: ['int', 'int'], result: 'int', apply: (a: number, b: number) => a - b } as OpSpec,
  '*': { args: ['int', 'int'], result: 'int', apply: (a: number, b: number) => a * b } as OpSpec,
  '<': { args: ['int', 'int'], result: 'bool', apply: (a: number, b: number) => a < b } as OpSpec,
  '>': { args: ['int', 'int'], result: 'bool', apply: (a: number, b: number) => a > b } as OpSpec,
  '==': { args: 'same', result: 'bool', apply: (a: unknown, b: unknown) => a === b } as OpSpec,
  '&&': { args: ['bool', 'bool'], result: 'bool', apply: (a: boolean, b: boolean) => a && b } as OpSpec,
  '||': { args: ['bool', 'bool'], result: 'bool', apply: (a: boolean, b: boolean) => a || b } as OpSpec,
};

/** Builds the attribute grammar for the demo language. */
export function buildLangGrammar(): AttributeGrammar {
  const g = new AttributeGrammar();

  g.define('Program', {
    attr: 'globalEnv',
    kind: 'synthesized',
    dependsOn: () => [],
    compute: (): Env => new Map<string, EnvEntry>(),
  });

  g.define('VarDecl', stmtEnvRule)
    .define('VarDecl', {
      attr: 'envOut',
      kind: 'synthesized',
      dependsOn: () => [self('env'), child(0, 'value')],
      compute: (node, ctx): Env => {
        const next = new Map(ctx.get<Env>(self('env')));
        next.set(node.field<string>('name'), {
          type: node.field<TypeName>('declaredType'),
          value: ctx.get(child(0, 'value')),
        });
        return next;
      },
    })
    .define('VarDecl', {
      attr: 'check',
      kind: 'synthesized',
      dependsOn: () => [child(0, 'type')],
      compute: (node, ctx): boolean => {
        const declared = node.field<TypeName>('declaredType');
        const actual = ctx.get<TypeName>(child(0, 'type'));
        if (actual !== 'error' && actual !== declared) {
          ctx.report(
            `type mismatch: cannot initialize variable '${node.field<string>('name')}' ` +
              `of type '${declared}' with expression of type '${actual}'`,
          );
        }
        return actual === declared;
      },
    });

  g.define('ExprStmt', stmtEnvRule)
    .define('ExprStmt', {
      attr: 'envOut',
      kind: 'synthesized',
      dependsOn: () => [self('env')],
      compute: (_node, ctx): Env => ctx.get<Env>(self('env')),
    })
    .define('ExprStmt', {
      attr: 'value',
      kind: 'synthesized',
      dependsOn: () => [child(0, 'value')],
      compute: (_node, ctx) => ctx.get(child(0, 'value')),
    });

  g.define('Num', {
    attr: 'type',
    kind: 'synthesized',
    dependsOn: () => [],
    compute: (): TypeName => 'int',
  }).define('Num', {
    attr: 'value',
    kind: 'synthesized',
    dependsOn: () => [],
    compute: (node) => node.field<number>('value'),
  });

  g.define('Bool', {
    attr: 'type',
    kind: 'synthesized',
    dependsOn: () => [],
    compute: (): TypeName => 'bool',
  }).define('Bool', {
    attr: 'value',
    kind: 'synthesized',
    dependsOn: () => [],
    compute: (node) => node.field<boolean>('value'),
  });

  g.define('Var', exprEnvRule)
    .define('Var', {
      attr: 'type',
      kind: 'synthesized',
      dependsOn: () => [self('env')],
      compute: (node, ctx): TypeName => {
        const entry = ctx.get<Env>(self('env')).get(node.field<string>('name'));
        if (!entry) {
          ctx.report(`undefined variable '${node.field<string>('name')}'`);
          return 'error';
        }
        return entry.type;
      },
    })
    .define('Var', {
      attr: 'value',
      kind: 'synthesized',
      dependsOn: () => [self('env')],
      compute: (node, ctx) => ctx.get<Env>(self('env')).get(node.field<string>('name'))?.value,
    });

  g.define('Binary', exprEnvRule)
    .define('Binary', {
      attr: 'type',
      kind: 'synthesized',
      dependsOn: () => [child(0, 'type'), child(1, 'type')],
      compute: (node, ctx): TypeName => {
        const op = node.field<Op>('op');
        const spec = OPS[op];
        const left = ctx.get<TypeName>(child(0, 'type'));
        const right = ctx.get<TypeName>(child(1, 'type'));
        if (left === 'error' || right === 'error') return 'error';
        const ok =
          spec.args === 'same'
            ? left === right
            : left === spec.args[0] && right === spec.args[1];
        if (!ok) {
          const want =
            spec.args === 'same' ? 'operands of the same type' : `(${spec.args.join(', ')})`;
          ctx.report(
            `type mismatch: operator '${op}' expects ${want} but got (${left}, ${right})`,
          );
          return 'error';
        }
        return spec.result;
      },
    })
    .define('Binary', {
      attr: 'value',
      kind: 'synthesized',
      dependsOn: () => [child(0, 'value'), child(1, 'value'), self('type')],
      compute: (node, ctx) => {
        if (ctx.get<TypeName>(self('type')) === 'error') return undefined;
        const spec = OPS[node.field<Op>('op')];
        return spec.apply(
          ctx.get(child(0, 'value')) as never,
          ctx.get(child(1, 'value')) as never,
        );
      },
    });

  return g;
}
