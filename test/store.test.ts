import assert from 'node:assert/strict';
import test from 'node:test';
import { CycleError, FormStore, UnknownFieldError } from '../src/index.ts';
import type { RuleContext, ValidateResult } from '../src/index.ts';

interface Deferred<T> {
  promise: Promise<T>;
  resolve(value: T | PromiseLike<T>): void;
  reject(reason?: unknown): void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Flushes the microtask queue plus one macrotask so commits can land. */
function flush(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

/** Async validator whose every call is gated on a manually resolved deferred. */
function manualValidator() {
  const calls: Array<{ value: unknown; gate: Deferred<ValidateResult> }> = [];
  const validate = (value: unknown): Promise<ValidateResult> => {
    const gate = deferred<ValidateResult>();
    calls.push({ value, gate });
    return gate.promise;
  };
  return { calls, validate };
}

test('hidden field keeps its value but skips validation', () => {
  const store = new FormStore(
    {
      mode: {},
      secret: {
        deps: ['mode'],
        visibleWhen: (ctx) => ctx.value('mode') === 'advanced',
        validate: (value) => (value ? null : 'required'),
      },
    },
    { mode: 'advanced', secret: '' },
  );

  assert.deepEqual(store.snapshot('secret').errors, ['required']);

  store.setValue('mode', 'basic');
  assert.equal(store.snapshot('secret').visible, false);
  assert.deepEqual(store.snapshot('secret').errors, []);
  assert.equal(store.snapshot('secret').value, '');

  store.setValue('secret', 's3cret'); // writing while hidden is allowed
  assert.equal(store.snapshot('secret').visible, false);
  assert.deepEqual(store.snapshot('secret').errors, []);

  store.setValue('mode', 'advanced');
  assert.equal(store.snapshot('secret').visible, true);
  assert.equal(store.snapshot('secret').value, 's3cret');
  assert.deepEqual(store.snapshot('secret').errors, []);
});

test('one setValue propagates through a dependency chain to a stable point', () => {
  const store = new FormStore(
    {
      a: {},
      b: { deps: ['a'], visibleWhen: (ctx) => Number(ctx.value('a')) > 0 },
      c: { deps: ['b'], visibleWhen: (ctx) => ctx.visible('b') },
    },
    { a: 0 },
  );

  assert.equal(store.snapshot('b').visible, false);
  assert.equal(store.snapshot('c').visible, false);

  store.setValue('a', 5);
  assert.equal(store.snapshot('b').visible, true);
  assert.equal(store.snapshot('c').visible, true);

  store.setValue('a', 0);
  assert.equal(store.snapshot('b').visible, false);
  assert.equal(store.snapshot('c').visible, false);
});

test('cyclic rule update is rejected as a whole, previous rules stay in effect', () => {
  const store = new FormStore({ a: {}, b: {}, c: {} });

  assert.throws(
    () =>
      store.updateRules({
        a: { deps: ['b'], visibleWhen: () => true },
        b: { deps: ['a'], visibleWhen: () => true },
        c: { validate: () => 'should never apply' },
      }),
    CycleError,
  );

  // Nothing from the batch was applied: c still has no validator...
  store.setValue('c', 'x');
  assert.deepEqual(store.snapshot('c').errors, []);
  // ...and a has no deps, so pointing b at a is still legal.
  store.updateRules({ b: { deps: ['a'], visibleWhen: () => true } });
  assert.equal(store.snapshot('b').visible, true);
});

test('self-dependency is rejected as a cycle', () => {
  const store = new FormStore({ a: {} });
  assert.throws(() => store.updateRules({ a: { deps: ['a'] } }), CycleError);
  assert.equal(store.snapshot('a').visible, true);
});

test('cyclic initial schema is rejected', () => {
  assert.throws(
    () =>
      new FormStore({
        a: { deps: ['b'] },
        b: { deps: ['a'] },
      }),
    CycleError,
  );
});

test('dependencies on unknown fields are rejected', () => {
  assert.throws(
    () => new FormStore({ a: { deps: ['ghost'] } }),
    UnknownFieldError,
  );
  const store = new FormStore({ a: {} });
  assert.throws(() => store.updateRules({ a: { deps: ['ghost'] } }), UnknownFieldError);
});

test('async validation commits only the result matching the current value version', async () => {
  const validator = manualValidator();
  const store = new FormStore(
    { code: { validate: validator.validate } },
    { code: 'free' },
  );
  assert.equal(validator.calls.length, 1); // initial stabilize
  validator.calls[0].gate.resolve(null);
  await store.whenSettled();

  store.setValue('code', 'taken'); // call 1 pending
  store.setValue('code', 'free'); // call 2 pending
  assert.equal(store.snapshot('code').validating, true);

  validator.calls[1].gate.resolve('already taken'); // stale: value moved on
  await flush();
  assert.deepEqual(store.snapshot('code').errors, []);

  validator.calls[2].gate.resolve(null); // current version commits
  await store.whenSettled();
  assert.equal(store.snapshot('code').validating, false);
  assert.deepEqual(store.snapshot('code').errors, []);

  store.setValue('code', 'taken'); // call 3
  validator.calls[3].gate.resolve('already taken');
  await store.whenSettled();
  assert.deepEqual(store.snapshot('code').errors, ['already taken']);
});

test('errors disappear when the field hides, stale async error is discarded', async () => {
  const validator = manualValidator();
  const store = new FormStore(
    {
      mode: {},
      name: {
        deps: ['mode'],
        visibleWhen: (ctx: RuleContext) => ctx.value('mode') === 'named',
        validate: validator.validate,
      },
    },
    { mode: 'named', name: 'bad' },
  );

  validator.calls[0].gate.resolve('invalid name');
  await store.whenSettled();
  assert.deepEqual(store.snapshot('name').errors, ['invalid name']);

  store.setValue('name', 'still-bad'); // call 1 pending
  store.setValue('mode', 'anon'); // hides the field synchronously
  assert.equal(store.snapshot('name').visible, false);
  assert.deepEqual(store.snapshot('name').errors, []);

  validator.calls[1].gate.resolve('invalid name'); // stale: field hidden
  await store.whenSettled();
  assert.deepEqual(store.snapshot('name').errors, []);

  store.setValue('mode', 'named'); // visible again, value retained
  assert.equal(store.snapshot('name').value, 'still-bad');
  validator.calls[2].gate.resolve('invalid name');
  await store.whenSettled();
  assert.deepEqual(store.snapshot('name').errors, ['invalid name']);
});

test('switching rules discards in-flight async validation', async () => {
  const validator = manualValidator();
  const store = new FormStore(
    { code: { validate: validator.validate } },
    { code: 'x' },
  );
  assert.equal(validator.calls.length, 1); // in-flight under old rules

  store.updateRules({ code: { validate: () => 'sync failure' } });
  assert.deepEqual(store.snapshot('code').errors, ['sync failure']);

  validator.calls[0].gate.resolve('stale async failure');
  await store.whenSettled();
  assert.deepEqual(store.snapshot('code').errors, ['sync failure']);

  store.updateRules({ code: null }); // removing rules clears errors
  assert.deepEqual(store.snapshot('code').errors, []);
});

test('full topological recompute revalidates fields that read other values', () => {
  const store = new FormStore(
    {
      password: {},
      confirm: {
        deps: ['password'],
        validate: (value, ctx) =>
          value === ctx.value('password') ? null : 'mismatch',
      },
    },
    { password: 'a', confirm: 'a' },
  );

  assert.deepEqual(store.snapshot('confirm').errors, []);

  store.setValue('password', 'b'); // confirm revalidated without being touched
  assert.deepEqual(store.snapshot('confirm').errors, ['mismatch']);

  store.setValue('confirm', 'b');
  assert.deepEqual(store.snapshot('confirm').errors, []);
});

test('visibility rules may depend on other fields visibility', () => {
  const store = new FormStore(
    {
      kind: {},
      detail: { deps: ['kind'], visibleWhen: (ctx) => ctx.value('kind') === 'full' },
      extra: { deps: ['detail'], visibleWhen: (ctx) => ctx.visible('detail') },
    },
    { kind: 'full' },
  );
  assert.equal(store.snapshot('extra').visible, true);

  store.setValue('kind', 'brief');
  assert.equal(store.snapshot('detail').visible, false);
  assert.equal(store.snapshot('extra').visible, false);
});

test('subscribers are notified on changes and async commits', async () => {
  const validator = manualValidator();
  const store = new FormStore({ a: {}, code: { validate: validator.validate } });
  let notifications = 0;
  const unsubscribe = store.subscribe(() => notifications++);

  store.setValue('a', 1);
  assert.equal(notifications, 1);

  validator.calls[1].gate.resolve('nope'); // commit notifies again
  await flush();
  assert.equal(notifications, 2);
  assert.deepEqual(store.snapshot('code').errors, ['nope']);

  validator.calls[0].gate.resolve(null); // initial run, stale by now
  await store.whenSettled();
  assert.equal(notifications, 3); // settling a stale run still emits

  unsubscribe();
  store.setValue('a', 2);
  assert.equal(notifications, 3);
});
