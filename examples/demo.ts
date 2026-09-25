import { movePaths, MoveConflictError } from '../src/index.ts';
import type { JsonValue } from '../src/index.ts';

const show = (label: string, value: unknown): void => {
  console.log(`${label}\n${JSON.stringify(value, null, 2)}\n`);
};

// 1. Wildcard + capture: a/* -> b/*
{
  const input = { a: { x: 1, y: [true, null] }, keep: 'untouched' };
  const { tree, plan } = movePaths(input, [{ from: 'a/*', to: 'b/*' }]);
  show('1) a/* -> b/*  plan:', plan.map((op) => `${op.fromPath}  =>  ${op.toPath}`));
  show('1) result:', tree);
}

// 2. Keys containing slashes, escaped as \/
{
  const input = { 'a/b': { 'c/d': 42 } };
  const { tree, plan } = movePaths(input, [{ from: 'a\\/b/*', to: 'out/*' }]);
  show('2) escaped key plan:', plan.map((op) => `${op.fromPath}  =>  ${op.toPath}`));
  show('2) result:', tree);
}

// 3. Ancestor -> descendant terminates (no recursive rewriting)
{
  const { tree } = movePaths({ a: { x: 1 } }, [{ from: 'a', to: 'a/b' }]);
  show('3) a -> a/b result:', tree);
}

// 4. Conflict: target exists with a different value
{
  const input: JsonValue = { a: { x: 1 }, b: { x: 2 } };
  try {
    movePaths(input, [{ from: 'a/*', to: 'b/*' }]);
  } catch (err) {
    if (err instanceof MoveConflictError) {
      show('4) conflict detected:', {
        message: err.message,
        targetPath: err.targetPath,
        sourcePaths: err.sourcePaths,
      });
    } else {
      throw err;
    }
  }
}

// 5. Glob ** and arrays
{
  const input = { src: { pkg: { 'main.ts': 'code' } }, list: [1, 2, 3] };
  const { tree } = movePaths(input, [
    { from: 'src/**', to: 'dist/**' },
    { from: 'list/0', to: 'first' },
  ]);
  show('5) glob + array result:', tree);
}
