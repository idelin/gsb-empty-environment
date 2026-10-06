import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FormEngine } from '../src/form-engine.ts';

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (err: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  let reject!: (err: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

test('visibility depends on another field value', () => {
  const engine = new FormEngine(['mode', 'extra'], {
    extra: { visibleWhen: (ctx) => ctx.value('mode') === 'advanced' },
  });
  assert.equal(engine.getState('extra').visible, false);
  engine.setValue('mode', 'advanced');
  assert.equal(engine.getState('extra').visible, true);
  engine.setValue('mode', 'simple');
  assert.equal(engine.getState('extra').visible, false);
});

test('one change propagates through chains and diamonds to a stable point', () => {
  const engine = new FormEngine(['a', 'b', 'c', 'd'], {
    b: { visibleWhen: (ctx) => ctx.value('a') === 'go' },
    c: { visibleWhen: (ctx) => ctx.visible('b') },
    d: { visibleWhen: (ctx) => ctx.visible('b') && ctx.visible('c') },
  });
  assert.deepEqual(engine.dependencies(), {
    a: [],
    b: ['a'],
    c: ['b'],
    d: ['b'],
  });
  engine.setValue('a', 'go');
  assert.equal(engine.getState('b').visible, true);
  assert.equal(engine.getState('c').visible, true);
  assert.equal(engine.getState('d').visible, true);
  assert.deepEqual(engine.dependencies(), {
    a: [],
    b: ['a'],
    c: ['b'],
    d: ['b', 'c'],
  });
  engine.setValue('a', 'stop');
  assert.equal(engine.getState('b').visible, false);
  assert.equal(engine.getState('c').visible, false);
  assert.equal(engine.getState('d').visible, false);
});

test('hidden fields keep values but skip validation; errors disappear', () => {
  const engine = new FormEngine(['mode', 'email'], {
    email: {
      visibleWhen: (ctx) => ctx.value('mode') === 'advanced',
      validate: (value) => (value ? null : 'required'),
    },
  });
  engine.setValue('mode', 'advanced');
  assert.deepEqual(engine.getState('email').errors, ['required']);

  engine.setValue('email', 'a@b.c');
  assert.deepEqual(engine.getState('email').errors, []);

  engine.setValue('mode', 'simple');
  const hidden = engine.getState('email');
  assert.equal(hidden.visible, false);
  assert.deepEqual(hidden.errors, []);
  assert.equal(hidden.value, 'a@b.c');

  engine.setValue('email', '');
  assert.deepEqual(engine.getState('email').errors, []);

  engine.setValue('mode', 'advanced');
  assert.deepEqual(engine.getState('email').errors, ['required']);
});

test('sync validators reading other fields re-run on every change', () => {
  const engine = new FormEngine(
    [{ name: 'password', value: '' }, { name: 'confirm', value: '' }],
    {
      confirm: {
        validate: (value, ctx) =>
          value === ctx.value('password') ? null : 'mismatch',
      },
    },
  );
  engine.setValue('password', 'secret');
  assert.deepEqual(engine.getState('confirm').errors, ['mismatch']);
  engine.setValue('confirm', 'secret');
  assert.deepEqual(engine.getState('confirm').errors, []);
  engine.setValue('password', 'changed');
  assert.deepEqual(engine.getState('confirm').errors, ['mismatch']);
});

test('cyclic rule updates are rejected atomically', () => {
  const engine = new FormEngine(['a', 'b'], {
    a: { visibleWhen: (ctx) => ctx.value('b') !== 'hide' },
  });
  const stateBefore = {
    a: engine.getState('a'),
    b: engine.getState('b'),
    deps: engine.dependencies(),
  };

  const result = engine.setRules({
    a: { visibleWhen: (ctx) => ctx.visible('b') },
    b: { visibleWhen: (ctx) => ctx.visible('a') },
  });
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /circular dependency/);

  assert.deepEqual(engine.getState('a'), stateBefore.a);
  assert.deepEqual(engine.getState('b'), stateBefore.b);
  assert.deepEqual(engine.dependencies(), stateBefore.deps);

  engine.setValue('b', 'hide');
  assert.equal(engine.getState('a').visible, false);

  const selfCycle = engine.setRules({
    a: { visibleWhen: (ctx) => ctx.visible('a') },
  });
  assert.equal(selfCycle.ok, false);
  assert.equal(engine.getState('a').visible, false);
});

test('rule updates referencing unknown fields are rejected', () => {
  const engine = new FormEngine(['a']);
  const result = engine.setRules({ ghost: {} });
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /unknown field: ghost/);
});

test('switching rules recomputes visibility and validation from scratch', () => {
  const engine = new FormEngine(
    [{ name: 'kind', value: 'x' }, { name: 'target', value: '' }],
    {
      target: {
        visibleWhen: (ctx) => ctx.value('kind') === 'x',
        validate: (value) => (value ? null : 'required-x'),
      },
    },
  );
  assert.equal(engine.getState('target').visible, true);
  assert.deepEqual(engine.getState('target').errors, ['required-x']);

  const switched = engine.setRules({
    target: {
      visibleWhen: (ctx) => ctx.value('kind') === 'y',
      validate: (value) => (value ? null : 'required-y'),
    },
  });
  assert.equal(switched.ok, true);
  assert.equal(engine.getState('target').visible, false);
  assert.deepEqual(engine.getState('target').errors, []);

  engine.setValue('kind', 'y');
  assert.equal(engine.getState('target').visible, true);
  assert.deepEqual(engine.getState('target').errors, ['required-y']);

  const cleared = engine.setRules({});
  assert.equal(cleared.ok, true);
  assert.equal(engine.getState('target').visible, true);
  assert.deepEqual(engine.getState('target').errors, []);
  assert.deepEqual(engine.dependencies(), { kind: [], target: [] });
});

test('async validation commits only the result matching the current value version', async () => {
  const runs: Array<Deferred<string | null>> = [];
  const engine = new FormEngine([{ name: 'name', value: 'init' }], {
    name: {
      validateAsync: () => {
        const run = deferred<string | null>();
        runs.push(run);
        return run.promise;
      },
    },
  });
  assert.equal(runs.length, 1);
  runs[0].resolve(null);
  await tick();
  assert.equal(engine.getState('name').pending, false);

  engine.setValue('name', 'alice');
  engine.setValue('name', 'alicia');
  assert.equal(runs.length, 3);
  assert.equal(engine.getState('name').pending, true);

  runs[1].resolve('stale: alice taken');
  await tick();
  assert.deepEqual(engine.getState('name').errors, []);
  assert.equal(engine.getState('name').pending, true);

  runs[2].resolve('alicia taken');
  await tick();
  assert.deepEqual(engine.getState('name').errors, ['alicia taken']);
  assert.equal(engine.getState('name').pending, false);
});

test('a late async result arriving after a newer commit is discarded', async () => {
  const runs: Array<Deferred<string | null>> = [];
  const engine = new FormEngine(['name'], {
    name: {
      validateAsync: () => {
        const run = deferred<string | null>();
        runs.push(run);
        return run.promise;
      },
    },
  });
  runs[0].resolve(null);
  await tick();

  engine.setValue('name', 'v1');
  engine.setValue('name', 'v2');
  runs[2].resolve('v2 error');
  await tick();
  assert.deepEqual(engine.getState('name').errors, ['v2 error']);

  runs[1].resolve('v1 error');
  await tick();
  assert.deepEqual(engine.getState('name').errors, ['v2 error']);
});

test('async errors disappear when inputs change and the new result commits', async () => {
  const runs: Array<Deferred<string | null>> = [];
  const engine = new FormEngine(['name'], {
    name: {
      validateAsync: () => {
        const run = deferred<string | null>();
        runs.push(run);
        return run.promise;
      },
    },
  });
  runs[0].resolve('init taken');
  await tick();
  assert.deepEqual(engine.getState('name').errors, ['init taken']);

  engine.setValue('name', 'fresh');
  assert.deepEqual(engine.getState('name').errors, []);
  assert.equal(engine.getState('name').pending, true);

  runs[1].resolve(null);
  await tick();
  assert.deepEqual(engine.getState('name').errors, []);
  assert.equal(engine.getState('name').pending, false);
});

test('async results are discarded when the field is hidden before resolve', async () => {
  const runs: Array<Deferred<string | null>> = [];
  const engine = new FormEngine(['mode', 'name'], {
    name: {
      visibleWhen: (ctx) => ctx.value('mode') === 'edit',
      validateAsync: () => {
        const run = deferred<string | null>();
        runs.push(run);
        return run.promise;
      },
    },
  });
  assert.equal(runs.length, 0);

  engine.setValue('mode', 'edit');
  assert.equal(runs.length, 1);
  assert.equal(engine.getState('name').pending, true);

  engine.setValue('mode', 'view');
  assert.equal(engine.getState('name').pending, false);

  runs[0].resolve('too late');
  await tick();
  assert.deepEqual(engine.getState('name').errors, []);
  assert.equal(engine.getState('name').pending, false);
});

test('committed async errors disappear when the field is hidden', async () => {
  const runs: Array<Deferred<string | null>> = [];
  const engine = new FormEngine([{ name: 'mode', value: 'edit' }, 'name'], {
    name: {
      visibleWhen: (ctx) => ctx.value('mode') === 'edit',
      validateAsync: () => {
        const run = deferred<string | null>();
        runs.push(run);
        return run.promise;
      },
    },
  });
  runs[0].resolve('bad');
  await tick();
  assert.deepEqual(engine.getState('name').errors, ['bad']);

  engine.setValue('mode', 'view');
  assert.deepEqual(engine.getState('name').errors, []);
});

test('async validators depending on other fields re-run when those change', async () => {
  const runs: Array<{ tenant: unknown; run: Deferred<string | null> }> = [];
  const engine = new FormEngine(
    [{ name: 'tenant', value: 'root' }, { name: 'user', value: 'admin' }],
    {
      user: {
        validateAsync: (value, ctx) => {
          const tenant = ctx.value('tenant');
          const run = deferred<string | null>();
          runs.push({ tenant, run });
          return run.promise;
        },
      },
    },
  );
  assert.equal(runs.length, 1);
  runs[0].run.resolve(null);
  await tick();

  engine.setValue('tenant', 'acme');
  assert.equal(runs.length, 2);
  assert.equal(runs[1].tenant, 'acme');
  assert.equal(engine.getState('user').pending, true);

  runs[1].run.resolve('admin not allowed outside root');
  await tick();
  assert.deepEqual(engine.getState('user').errors, [
    'admin not allowed outside root',
  ]);
});

test('async validator rejections surface as error messages', async () => {
  const runs: Array<Deferred<string | null>> = [];
  const engine = new FormEngine(['name'], {
    name: {
      validateAsync: () => {
        const run = deferred<string | null>();
        runs.push(run);
        return run.promise;
      },
    },
  });
  runs[0].reject(new Error('network down'));
  await tick();
  assert.deepEqual(engine.getState('name').errors, ['network down']);
});

test('whenSettled resolves once all pending validations finish', async () => {
  const runs: Array<Deferred<string | null>> = [];
  const engine = new FormEngine(['name'], {
    name: {
      validateAsync: () => {
        const run = deferred<string | null>();
        runs.push(run);
        return run.promise;
      },
    },
  });
  let settled = false;
  const waiting = engine.whenSettled().then(() => {
    settled = true;
  });
  await tick();
  assert.equal(settled, false);
  runs[0].resolve(null);
  await waiting;
  assert.equal(settled, true);
  await engine.whenSettled();
});

test('subscribers are notified of changes and async commits', async () => {
  const runs: Array<Deferred<string | null>> = [];
  const engine = new FormEngine(['name'], {
    name: {
      validateAsync: () => {
        const run = deferred<string | null>();
        runs.push(run);
        return run.promise;
      },
    },
  });
  let notifications = 0;
  const unsubscribe = engine.subscribe(() => {
    notifications += 1;
  });
  engine.setValue('name', 'x');
  assert.equal(notifications, 1);
  runs[1].resolve(null);
  await tick();
  assert.equal(notifications, 2);
  unsubscribe();
  engine.setValue('name', 'y');
  assert.equal(notifications, 2);
});
