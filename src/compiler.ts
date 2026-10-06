import { Node, Span } from './ast';
import { EngineHost, RenderContext, Runtime, Scopes } from './runtime';

/** A compiled template: structurally identical semantics to the interpreter. */
export type CompiledFn = (
  rt: Runtime,
  engine: EngineHost,
  scopes: Scopes,
  ctx: RenderContext,
) => void;

/**
 * Compile an AST to a JavaScript function. Control flow (if/each) becomes
 * native JS control flow, so untaken branches are never executed and loop
 * frames are pushed/popped exactly like the interpreter does. All value
 * semantics go through the same shared runtime helpers.
 */
export function compile(nodes: Node[]): CompiledFn {
  const body = genNodes(nodes, '  ');
  const source = `'use strict';\nconst out = ctx.fragments;\n${body}`;
  const fn = new Function('rt', 'engine', 'scopes', 'ctx', source) as CompiledFn;
  return fn;
}

function genNodes(nodes: Node[], indent: string): string {
  const lines: string[] = [];
  for (const node of nodes) {
    switch (node.kind) {
      case 'text':
        lines.push(
          `${indent}out.push({ text: ${JSON.stringify(node.text)}, span: ${lit(node.span)} });`,
        );
        break;
      case 'var':
        lines.push(
          `${indent}out.push({ text: rt.stringify(rt.resolve(scopes, ${JSON.stringify(node.path)}, ${lit(node.span)}, ctx)), span: ${lit(node.span)} });`,
        );
        break;
      case 'if': {
        lines.push(
          `${indent}if (rt.truthy(rt.resolve(scopes, ${JSON.stringify(node.path)}, ${lit(node.span)}, ctx))) {`,
        );
        lines.push(genNodes(node.then, indent + '  '));
        if (node.else.length > 0) {
          lines.push(`${indent}} else {`);
          lines.push(genNodes(node.else, indent + '  '));
        }
        lines.push(`${indent}}`);
        break;
      }
      case 'each': {
        lines.push(
          `${indent}rt.each(rt.resolve(scopes, ${JSON.stringify(node.path)}, ${lit(node.span)}, ctx), ${lit(node.span)}, ctx, function (item, index) {`,
        );
        lines.push(
          `${indent}  scopes.push({ ${JSON.stringify(node.itemName)}: item, '@index': index });`,
        );
        lines.push(`${indent}  try {`);
        lines.push(genNodes(node.body, indent + '    '));
        lines.push(`${indent}  } finally {`);
        lines.push(`${indent}    scopes.pop();`);
        lines.push(`${indent}  }`);
        lines.push(`${indent}});`);
        break;
      }
      case 'include':
        lines.push(
          `${indent}rt.include(engine, ctx, scopes, ${JSON.stringify(node.name)}, ${lit(node.span)});`,
        );
        break;
    }
  }
  return lines.join('\n');
}

function lit(span: Span): string {
  return JSON.stringify(span);
}
