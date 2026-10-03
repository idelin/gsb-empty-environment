/**
 * Demo language defined on top of the attribute grammar kernel.
 *
 * Statements:  let-declarations, print, if-blocks (nested scopes)
 * Expressions: int/bool literals, variable references, binary operators
 *
 * Attributes:
 *   env      (inherited)  - scope environment { name -> { type, value } },
 *                           threaded top-down through the statement chain
 *   envAfter (synthesized)- environment after a statement (declarations bind)
 *   type     (synthesized)- expression / declaration type, computed bottom-up
 *   value    (synthesized)- constant-evaluated expression result
 *   output   (synthesized)- text produced by print statements
 */

import {
  AttributeGrammar,
  type ASTNode,
  type AttrRef,
  self,
  parent,
  child,
  direct,
} from './grammar.ts';
import { LangError } from './errors.ts';

export type TypeName = 'int' | 'bool';

export interface EnvEntry {
  type: TypeName;
  value?: unknown;
}

export type Env = Map<string, EnvEntry>;

const STATEMENT_KINDS = ['VarDecl', 'Print', 'If'] as const;
const EXPRESSION_KINDS = ['IntLit', 'BoolLit', 'VarRef', 'BinOp'] as const;

/**
 * Inherited `env` dependencies for a statement: the first statement of a
 * Program/Block inherits the parent's env; every later statement inherits the
 * previous sibling's `envAfter` (this is what threads declarations through a
 * scope, top-down, in source order).
 */
function stmtEnvDeps(node: ASTNode): AttrRef[] {
  const p = node.parent;
  if (p && (p.kind === 'Program' || p.kind === 'Block')) {
    const stmts = p.stmts as ASTNode[];
    const index = stmts.indexOf(node);
    if (index > 0) return [direct(stmts[index - 1], 'envAfter')];
  }
  return [parent('env')];
}

function lookupEnv(env: Env, name: string, node: ASTNode): EnvEntry {
  const entry = env.get(name);
  if (!entry) {
    throw new LangError(`undefined variable '${name}'`, node.pos);
  }
  return entry;
}

const ARITHMETIC = new Set(['+', '-', '*', '/']);
const COMPARISON = new Set(['<', '>', '<=', '>=']);
const EQUALITY = new Set(['==', '!=']);

function checkBinOpType(op: string, left: TypeName, right: TypeName, node: ASTNode): TypeName {
  if (ARITHMETIC.has(op) || COMPARISON.has(op)) {
    if (left === 'int' && right === 'int') {
      return ARITHMETIC.has(op) ? 'int' : 'bool';
    }
    throw new LangError(
      `operator '${op}' expects int operands, got ${left} and ${right}`,
      node.pos,
    );
  }
  if (EQUALITY.has(op)) {
    if (left === right) return 'bool';
    throw new LangError(
      `operator '${op}' expects operands of the same type, got ${left} and ${right}`,
      node.pos,
    );
  }
  throw new LangError(`unknown operator '${op}'`, node.pos);
}

function evalBinOp(op: string, left: unknown, right: unknown): unknown {
  switch (op) {
    case '+': return (left as number) + (right as number);
    case '-': return (left as number) - (right as number);
    case '*': return (left as number) * (right as number);
    case '/': return Math.trunc((left as number) / (right as number));
    case '<': return (left as number) < (right as number);
    case '>': return (left as number) > (right as number);
    case '<=': return (left as number) <= (right as number);
    case '>=': return (left as number) >= (right as number);
    case '==': return left === right;
    case '!=': return left !== right;
    default: throw new Error(`unknown operator '${op}'`);
  }
}

function joinOutputs(parts: unknown[]): string {
  return parts
    .map((part) => String(part))
    .filter((part) => part.length > 0)
    .join('\n');
}

export function buildLanguageGrammar(): AttributeGrammar {
  const grammar = new AttributeGrammar();

  // --- Attribute declarations ---------------------------------------------
  grammar.addAttribute('Program', 'env', 'inherited');
  grammar.addAttribute('Program', 'output', 'synthesized');

  grammar.addAttribute('Block', 'env', 'inherited');
  grammar.addAttribute('Block', 'output', 'synthesized');

  for (const kind of STATEMENT_KINDS) {
    grammar.addAttribute(kind, 'env', 'inherited');
    grammar.addAttribute(kind, 'envAfter', 'synthesized');
    grammar.addAttribute(kind, 'output', 'synthesized');
  }
  grammar.addAttribute('VarDecl', 'type', 'synthesized');

  for (const kind of EXPRESSION_KINDS) {
    grammar.addAttribute(kind, 'env', 'inherited');
    grammar.addAttribute(kind, 'type', 'synthesized');
    grammar.addAttribute(kind, 'value', 'synthesized');
  }

  // --- Program / Block ------------------------------------------------------
  grammar.addRule('Program', 'env', [], () => new Map<string, EnvEntry>());
  grammar.addRule(
    'Program',
    'output',
    (node) => (node.stmts as ASTNode[]).map((stmt) => direct(stmt, 'output')),
    (node, get) =>
      joinOutputs((node.stmts as ASTNode[]).map((stmt) => get(direct(stmt, 'output')))),
  );

  grammar.addRule('Block', 'env', [parent('env')], (_node, get) => get(parent('env')));
  grammar.addRule(
    'Block',
    'output',
    (node) => (node.stmts as ASTNode[]).map((stmt) => direct(stmt, 'output')),
    (node, get) =>
      joinOutputs((node.stmts as ASTNode[]).map((stmt) => get(direct(stmt, 'output')))),
  );

  // --- Statements: inherited env --------------------------------------------
  for (const kind of STATEMENT_KINDS) {
    grammar.addRule(kind, 'env', stmtEnvDeps, (node, get) => get(stmtEnvDeps(node)[0]));
  }

  // --- VarDecl --------------------------------------------------------------
  // Declared type: annotation checked against the initializer's synthesized
  // type; without an annotation the type is inferred bottom-up.
  grammar.addRule(
    'VarDecl',
    'type',
    [child('init', 'type')],
    (node, get) => {
      const initType = get(child('init', 'type')) as TypeName;
      const annotation = node.annotation as TypeName | undefined;
      if (annotation && annotation !== initType) {
        throw new LangError(
          `type mismatch: variable '${node.name}' is declared as ${annotation} but initialized with ${initType}`,
          node.pos,
        );
      }
      return annotation ?? initType;
    },
  );
  // envAfter: bind name -> { type, value } on top of the inherited env.
  grammar.addRule(
    'VarDecl',
    'envAfter',
    [self('env'), self('type'), child('init', 'value')],
    (node, get) => {
      const env = get(self('env')) as Env;
      const next: Env = new Map(env);
      next.set(node.name as string, {
        type: get(self('type')) as TypeName,
        value: get(child('init', 'value')),
      });
      return next;
    },
  );
  grammar.addRule('VarDecl', 'output', [], () => '');

  // --- Print ----------------------------------------------------------------
  grammar.addRule('Print', 'envAfter', [self('env')], (_node, get) => get(self('env')));
  grammar.addRule(
    'Print',
    'output',
    [child('expr', 'value'), child('expr', 'type')],
    (_node, get) => String(get(child('expr', 'value'))),
  );

  // --- If -------------------------------------------------------------------
  grammar.addRule('If', 'envAfter', [self('env')], (_node, get) => get(self('env')));
  grammar.addRule(
    'If',
    'output',
    (node) => {
      const deps: AttrRef[] = [child('cond', 'value'), child('cond', 'type'), child('then', 'output')];
      if (node.else) deps.push(child('else', 'output'));
      return deps;
    },
    (node, get) => {
      const condType = get(child('cond', 'type')) as TypeName;
      if (condType !== 'bool') {
        throw new LangError(`if condition must be bool, got ${condType}`, (node.cond as ASTNode).pos);
      }
      const condValue = get(child('cond', 'value')) as boolean;
      if (condValue) return get(child('then', 'output')) as string;
      return node.else ? (get(child('else', 'output')) as string) : '';
    },
  );

  // --- Expressions: inherited env -------------------------------------------
  for (const kind of EXPRESSION_KINDS) {
    grammar.addRule(kind, 'env', [parent('env')], (_node, get) => get(parent('env')));
  }

  // --- Literals ---------------------------------------------------------------
  grammar.addRule('IntLit', 'type', [], () => 'int');
  grammar.addRule('IntLit', 'value', [], (node) => node.value);

  grammar.addRule('BoolLit', 'type', [], () => 'bool');
  grammar.addRule('BoolLit', 'value', [], (node) => node.value);

  // --- Variable reference -----------------------------------------------------
  grammar.addRule('VarRef', 'type', [self('env')], (node, get) => {
    const env = get(self('env')) as Env;
    return lookupEnv(env, node.name as string, node).type;
  });
  grammar.addRule('VarRef', 'value', [self('env')], (node, get) => {
    const env = get(self('env')) as Env;
    return lookupEnv(env, node.name as string, node).value;
  });

  // --- Binary operation ---------------------------------------------------------
  grammar.addRule(
    'BinOp',
    'type',
    [child('left', 'type'), child('right', 'type')],
    (node, get) =>
      checkBinOpType(
        node.op as string,
        get(child('left', 'type')) as TypeName,
        get(child('right', 'type')) as TypeName,
        node,
      ),
  );
  grammar.addRule(
    'BinOp',
    'value',
    [child('left', 'value'), child('right', 'value')],
    (node, get) =>
      evalBinOp(node.op as string, get(child('left', 'value')), get(child('right', 'value'))),
  );

  return grammar;
}
