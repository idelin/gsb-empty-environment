import assert from 'node:assert/strict';

/** Run fn, assert it throws a ConfigError-like value, and return it. */
export function capture<T extends Error>(fn: () => void, ctor?: new (...args: never[]) => T): T {
  try {
    fn();
  } catch (err) {
    if (ctor) assert.ok(err instanceof ctor, `expected ${ctor.name}, got ${err}`);
    return err as T;
  }
  assert.fail('expected function to throw');
}
