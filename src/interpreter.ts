import { Node } from './ast';
import {
  EngineHost,
  RenderContext,
  Runtime,
  Scopes,
} from './runtime';

/**
 * The reference model: a direct tree-walking interpreter over the AST.
 * The compiler's output is verified against this engine.
 */
export function interpret(
  nodes: Node[],
  rt: Runtime,
  engine: EngineHost,
  scopes: Scopes,
  ctx: RenderContext,
): void {
  for (const node of nodes) {
    switch (node.kind) {
      case 'text':
        ctx.fragments.push({ text: node.text, span: node.span });
        break;
      case 'var':
        ctx.fragments.push({
          text: rt.stringify(rt.resolve(scopes, node.path, node.span, ctx)),
          span: node.span,
        });
        break;
      case 'if':
        // Only the taken branch is interpreted; the other branch's variables
        // are never resolved.
        if (rt.truthy(rt.resolve(scopes, node.path, node.span, ctx))) {
          interpret(node.then, rt, engine, scopes, ctx);
        } else {
          interpret(node.else, rt, engine, scopes, ctx);
        }
        break;
      case 'each':
        rt.each(rt.resolve(scopes, node.path, node.span, ctx), node.span, ctx, (item, index) => {
          // Push a fresh frame per iteration; the loop variable shadows any
          // outer binding and is removed on exit, restoring the outer scope.
          scopes.push({ [node.itemName]: item, '@index': index });
          try {
            interpret(node.body, rt, engine, scopes, ctx);
          } finally {
            scopes.pop();
          }
        });
        break;
      case 'include':
        rt.include(engine, ctx, scopes, node.name, node.span);
        break;
    }
  }
}
