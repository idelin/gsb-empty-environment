import type { Ast, Span, Tree } from './types.js';

export function* enumerateTrees(ast: Ast, input: string, pos: number): Generator<Tree, void, undefined> {
  switch (ast.kind) {
    case 'lit': {
      if (pos < input.length && input[pos] === ast.ch) {
        yield { kind: 'lit', start: pos, end: pos + 1 };
      }
      return;
    }
    case 'concat': {
      yield* enumerateConcat(ast.parts, 0, input, pos, pos, []);
      return;
    }
    case 'alt': {
      for (let choice = 0; choice < ast.options.length; choice++) {
        for (const child of enumerateTrees(ast.options[choice], input, pos)) {
          yield { kind: 'alt', choice, child, start: pos, end: child.end };
        }
      }
      return;
    }
    case 'star': {
      yield* enumerateStar(ast.inner, input, pos);
      return;
    }
    case 'group': {
      for (const child of enumerateTrees(ast.inner, input, pos)) {
        yield { kind: 'group', index: ast.index, child, start: pos, end: child.end };
      }
      return;
    }
  }
}

function* enumerateConcat(
  parts: readonly Ast[],
  index: number,
  input: string,
  start: number,
  pos: number,
  acc: readonly Tree[],
): Generator<Tree, void, undefined> {
  if (index === parts.length) {
    yield { kind: 'concat', children: acc, start, end: pos };
    return;
  }
  for (const child of enumerateTrees(parts[index], input, pos)) {
    yield* enumerateConcat(parts, index + 1, input, start, child.end, [...acc, child]);
  }
}

function* enumerateStar(inner: Ast, input: string, pos: number): Generator<Tree, void, undefined> {
  yield { kind: 'star', iterations: [], start: pos, end: pos };
  for (const first of enumerateTrees(inner, input, pos)) {
    if (first.end === pos) {
      yield { kind: 'star', iterations: [first], start: pos, end: pos };
    } else {
      for (const rest of enumerateStar(inner, input, first.end)) {
        yield { kind: 'star', iterations: [first, ...rest.iterations], start: pos, end: rest.end };
      }
    }
  }
}

export function treeCaptures(tree: Tree, groupCount: number): Array<Span | undefined> {
  const caps: Array<Span | undefined> = new Array(groupCount).fill(undefined);
  collectCaptures(tree, caps);
  return caps;
}

function collectCaptures(tree: Tree, caps: Array<Span | undefined>): void {
  switch (tree.kind) {
    case 'group': {
      caps[tree.index - 1] = [tree.start, tree.end];
      collectCaptures(tree.child, caps);
      return;
    }
    case 'concat': {
      for (const child of tree.children) collectCaptures(child, caps);
      return;
    }
    case 'alt': {
      collectCaptures(tree.child, caps);
      return;
    }
    case 'star': {
      for (const iteration of tree.iterations) collectCaptures(iteration, caps);
      return;
    }
    default: {
      return;
    }
  }
}
