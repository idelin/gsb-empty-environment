import { AstNode, at } from './ast';
import { AttributeGrammar, CycleError, Evaluator, self } from './grammar';
import {
  binary,
  bool,
  buildLangGrammar,
  exprStmt,
  formatDiagnostic,
  num,
  program,
  TypeName,
  varDecl,
  varRef,
} from './lang';

const evaluator = new Evaluator(buildLangGrammar());

/* Sample 1: a well-typed program.
 *   1: let x: int = 1 + 2;
 *   2: let y: bool = x < 5;
 *   3: x * 4;
 *   4: y && true;
 */
const ok = program([
  varDecl('x', 'int', binary('+', num(1, at(1, 16)), num(2, at(1, 20)), at(1, 18)), at(1, 5)),
  varDecl('y', 'bool', binary('<', varRef('x', at(2, 17)), num(5, at(2, 21)), at(2, 19)), at(2, 5)),
  exprStmt(binary('*', varRef('x', at(3, 1)), num(4, at(3, 5)), at(3, 3)), at(3, 1)),
  exprStmt(binary('&&', varRef('y', at(4, 1)), bool(true, at(4, 6)), at(4, 3)), at(4, 1)),
]);

const okResult = evaluator.evaluate(ok);
console.log('=== Sample 1: well-typed program ===');
for (const stmt of ok.children) {
  if (stmt.kind === 'VarDecl') {
    console.log(
      `declared ${stmt.field<string>('name')}: env entry =`,
      okResult.get(stmt, 'envOut') instanceof Map
        ? (okResult.get<Map<string, { type: TypeName }>>(stmt, 'envOut').get(stmt.field<string>('name')))
        : undefined,
    );
  } else {
    console.log(`expr stmt @${stmt.pos.line}: value =`, okResult.get(stmt, 'value'));
  }
}
console.log('diagnostics:', okResult.diagnostics.length === 0 ? '(none)' : okResult.diagnostics);

/* Sample 2: type errors reported with precise AST positions.
 *   1: let a: int = true;
 *   2: let b: int = 1;
 *   3: b + false;
 *   4: missing;
 */
const bad = program([
  varDecl('a', 'int', bool(true, at(1, 14)), at(1, 5)),
  varDecl('b', 'int', num(1, at(2, 14)), at(2, 5)),
  exprStmt(binary('+', varRef('b', at(3, 1)), bool(false, at(3, 5)), at(3, 3)), at(3, 1)),
  exprStmt(varRef('missing', at(4, 1)), at(4, 1)),
]);

const badResult = evaluator.evaluate(bad);
console.log('\n=== Sample 2: type errors ===');
for (const d of badResult.diagnostics) {
  console.log(`error at ${formatDiagnostic(d)}`);
}

/* Sample 3: circular attribute dependencies are detected. */
console.log('\n=== Sample 3: cycle detection ===');
const cyclic = new AttributeGrammar();
cyclic
  .define('Node', {
    attr: 'a',
    kind: 'synthesized',
    dependsOn: () => [self('b')],
    compute: (_n, ctx) => ctx.get(self('b')),
  })
  .define('Node', {
    attr: 'b',
    kind: 'synthesized',
    dependsOn: () => [self('a')],
    compute: (_n, ctx) => ctx.get(self('a')),
  });
try {
  new Evaluator(cyclic).evaluate(new AstNode('Node', at(1, 1)));
  console.log('ERROR: cycle was not detected');
} catch (err) {
  if (err instanceof CycleError) {
    console.log(`detected as expected: ${err.message}`);
  } else {
    throw err;
  }
}
