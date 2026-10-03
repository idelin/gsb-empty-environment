import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parse } from '../src/parser.ts';
import { buildLanguageGrammar, type Env, type TypeName } from '../src/language.ts';
import { LangError } from '../src/errors.ts';
import type { ASTNode } from '../src/grammar.ts';

const grammar = buildLanguageGrammar();

function evaluate(source: string) {
  const ast = parse(source);
  return { ast, result: grammar.evaluate(ast) };
}

function firstDecl(ast: ASTNode): ASTNode {
  return (ast.stmts as ASTNode[])[0];
}

test('infers int type for an arithmetic initializer', () => {
  const { ast, result } = evaluate('let x = 1 + 2 * 3;');
  const decl = firstDecl(ast);
  assert.equal(result.get<TypeName>(decl, 'type'), 'int');
  const env = result.get<Env>(decl, 'envAfter');
  assert.deepEqual(env.get('x'), { type: 'int', value: 7 });
});

test('infers bool type through a comparison of inferred ints', () => {
  const { ast, result } = evaluate('let x = 10;\nlet b = x > 5;');
  const decl = (ast.stmts as ASTNode[])[1];
  assert.equal(result.get<TypeName>(decl, 'type'), 'bool');
  assert.equal(result.get<Env>(decl, 'envAfter').get('b')?.value, true);
});

test('accepts a matching explicit annotation', () => {
  const { ast, result } = evaluate('let x: int = 40 + 2;');
  assert.equal(result.get<TypeName>(firstDecl(ast), 'type'), 'int');
});

test('annotation mismatch is reported at the declaration position', () => {
  assert.throws(
    () => evaluate('let a = 1;\nlet b: bool = a + 2;'),
    (error: unknown) => {
      assert.ok(error instanceof LangError);
      assert.match(error.message, /declared as bool but initialized with int/);
      assert.deepEqual({ line: error.pos.line, col: error.pos.col }, { line: 2, col: 1 });
      return true;
    },
  );
});

test('operator operand mismatch is reported at the operator position', () => {
  // the `+` sits at line 2, column 11 and is the BinOp node position.
  assert.throws(
    () => evaluate('let flag = true;\nlet n = 1 + flag;'),
    (error: unknown) => {
      assert.ok(error instanceof LangError);
      assert.match(error.message, /operator '\+' expects int operands, got int and bool/);
      assert.deepEqual({ line: error.pos.line, col: error.pos.col }, { line: 2, col: 11 });
      return true;
    },
  );
});

test('undefined variable is reported at the use site', () => {
  assert.throws(
    () => evaluate('let x = 1;\nprint x + y;'),
    (error: unknown) => {
      assert.ok(error instanceof LangError);
      assert.match(error.message, /undefined variable 'y'/);
      assert.deepEqual({ line: error.pos.line, col: error.pos.col }, { line: 2, col: 11 });
      return true;
    },
  );
});

test('inherited env threads declarations to later statements', () => {
  const { ast, result } = evaluate('let x = 3;\nlet y = x * 2;\nprint y;');
  assert.equal(result.get<string>(ast, 'output'), '6');
});

test('inner scope can shadow an outer variable', () => {
  const source = 'let x = 1;\nif true {\n  let x = false;\n  print x;\n}\nprint x;';
  const { ast, result } = evaluate(source);
  assert.equal(result.get<string>(ast, 'output'), 'false\n1');
});

test('inner declarations do not leak out of their block', () => {
  assert.throws(
    () => evaluate('if true {\n  let inner = 42;\n}\nprint inner;'),
    (error: unknown) => {
      assert.ok(error instanceof LangError);
      assert.match(error.message, /undefined variable 'inner'/);
      assert.deepEqual({ line: error.pos.line, col: error.pos.col }, { line: 4, col: 7 });
      return true;
    },
  );
});

test('if condition must be bool', () => {
  assert.throws(
    () => evaluate('if 1 + 2 {\n  print 1;\n}'),
    (error: unknown) => {
      assert.ok(error instanceof LangError);
      assert.match(error.message, /if condition must be bool, got int/);
      return true;
    },
  );
});

test('equality requires matching operand types', () => {
  assert.throws(
    () => evaluate('let x = 1 == true;'),
    /operator '==' expects operands of the same type, got int and bool/,
  );
});

test('end-to-end program output via synthesized attributes', () => {
  const source = [
    'let x: int = 1 + 2;',
    'let y = x * 10;',
    'let ok: bool = y > 5;',
    'if ok {',
    '  let z = y + x;',
    '  print z;',
    '} else {',
    '  print 0;',
    '}',
    'print ok;',
  ].join('\n');
  const { ast, result } = evaluate(source);
  assert.equal(result.get<string>(ast, 'output'), '33\ntrue');
});
