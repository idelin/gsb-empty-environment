/**
 * End-to-end sample: parse a mini program, evaluate its attributes
 * (inherited env top-down, synthesized type/value/output bottom-up),
 * print inferred types and program output, then show precise diagnostics
 * for two ill-typed programs.
 */

import { parse } from '../src/parser.ts';
import { buildLanguageGrammar, type Env, type TypeName } from '../src/language.ts';
import type { ASTNode } from '../src/grammar.ts';
import { LangError } from '../src/errors.ts';

const grammar = buildLanguageGrammar();

function run(title: string, source: string): void {
  console.log(`=== ${title} ${'='.repeat(Math.max(0, 60 - title.length))}`);
  console.log(source.trim());
  console.log('-'.repeat(64));
  try {
    const ast = parse(source);
    const result = grammar.evaluate(ast);

    // Report inferred / checked declaration types.
    const decls: ASTNode[] = [];
    const collect = (node: ASTNode): void => {
      if (node.kind === 'VarDecl') decls.push(node);
      for (const key of Object.keys(node)) {
        if (key === 'parent') continue;
        const value = node[key];
        if (Array.isArray(value)) value.forEach((item) => item && item.kind && collect(item as ASTNode));
        else if (value && (value as ASTNode).kind) collect(value as ASTNode);
      }
    };
    collect(ast);
    for (const decl of decls) {
      const type = result.get<TypeName>(decl, 'type');
      const env = result.get<Env>(decl, 'envAfter');
      const entry = env.get(decl.name as string);
      console.log(
        `decl ${decl.name} : ${type} = ${JSON.stringify(entry?.value)}` +
        `  (declared at ${decl.pos.line}:${decl.pos.col})`,
      );
    }

    const output = result.get<string>(ast, 'output');
    if (output.length > 0) {
      console.log('--- program output ---');
      console.log(output);
    }
  } catch (error) {
    if (error instanceof LangError) {
      console.log(`error: ${error.message}`);
    } else {
      throw error;
    }
  }
  console.log();
}

run('well-typed program', `
let x: int = 1 + 2;
let y = x * 10;
let ok: bool = y > 5;
if ok {
  let z = y + x;
  print z;
} else {
  print 0;
}
print ok;
`);

run('annotation mismatch (position of the declaration)', `
let a = 1;
let b: bool = a + 2;
`);

run('operator type mismatch (position of the operator)', `
let flag = true;
let n = 40 +
  flag;
`);

run('undefined variable (position of the use site)', `
let x = 1;
print x + y;
`);

run('scope leak (inner declaration not visible outside)', `
if true {
  let inner = 42;
  print inner;
}
print inner;
`);
