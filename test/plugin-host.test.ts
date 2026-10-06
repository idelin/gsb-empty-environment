import assert from 'assert/strict';
import {
  CleanupError,
  PluginCycleError,
  PluginDefinition,
  PluginHost,
  PluginInstallError,
  UnknownPluginError,
} from '../src/index';
import { ControllableFactory, EventLog, runTests, test } from './helpers';

function loggingPlugin(
  name: string,
  log: EventLog,
  options: {
    dependencies?: readonly string[];
    failDispose?: boolean;
  } = {},
): PluginDefinition<{ name: string }> {
  return {
    name,
    ...(options.dependencies !== undefined
      ? { dependencies: options.dependencies }
      : {}),
    factory: () => {
      log.record(`init:${name}`);
      return { name };
    },
    dispose: () => {
      log.record(`dispose:${name}`);
      if (options.failDispose === true) {
        throw new Error(`cannot dispose ${name}`);
      }
    },
  };
}

test('initializes plugins in dependency order', async () => {
  const host = new PluginHost();
  const log = new EventLog();
  await host.install([
    loggingPlugin('c', log, { dependencies: ['b'] }),
    loggingPlugin('b', log, { dependencies: ['a'] }),
    loggingPlugin('a', log),
  ]);
  assert.deepEqual(log.filtered('init:'), ['init:a', 'init:b', 'init:c']);
  assert.equal(host.isReady('c'), true);
});

test('injects dependency instances into factories via context', async () => {
  const host = new PluginHost();
  await host.install([
    { name: 'config', factory: () => ({ port: 8080 }) },
    {
      name: 'server',
      dependencies: ['config'],
      factory: (ctx) => ({ port: ctx.get<{ port: number }>('config').port }),
    },
  ]);
  assert.deepEqual(host.get('server'), { port: 8080 });
});

test('rolls back only instances created by the failing install', async () => {
  const host = new PluginHost();
  const log = new EventLog();
  await host.install([loggingPlugin('shared', log)]);

  const boom = new Error('boom');
  const attempt = host.install([
    loggingPlugin('mid', log, { dependencies: ['shared'] }),
    {
      name: 'broken',
      dependencies: ['mid'],
      factory: () => Promise.reject(boom),
    },
  ]);
  await assert.rejects(attempt, (error: unknown) => {
    if (!(error instanceof PluginInstallError)) {
      throw error;
    }
    assert.equal(error.plugin, 'broken');
    assert.equal(error.cause, boom);
    return true;
  });

  // 'mid' was created by this install and is rolled back;
  // 'shared' pre-existed and is kept.
  assert.deepEqual(log.filtered('dispose:'), ['dispose:mid']);
  assert.equal(host.isReady('shared'), true);
  assert.equal(host.isReady('mid'), false);
  assert.equal(host.refCount('shared'), 1);
  assert.equal(host.refCount('mid'), 0);
});

test('rolls back a chain in reverse order when a later plugin fails', async () => {
  const host = new PluginHost();
  const log = new EventLog();
  const attempt = host.install([
    loggingPlugin('a', log),
    loggingPlugin('b', log, { dependencies: ['a'] }),
    {
      name: 'c',
      dependencies: ['b'],
      factory: () => {
        throw new Error('c failed');
      },
    },
  ]);
  await assert.rejects(attempt, PluginInstallError);
  assert.deepEqual(log.filtered('dispose:'), ['dispose:b', 'dispose:a']);
  assert.equal(host.isReady('a'), false);
  assert.equal(host.isReady('b'), false);
});

test('uninstall disposes in reverse dependency order', async () => {
  const host = new PluginHost();
  const log = new EventLog();
  const handle = await host.install([
    loggingPlugin('a', log),
    loggingPlugin('b', log, { dependencies: ['a'] }),
    loggingPlugin('c', log, { dependencies: ['b'] }),
  ]);
  await host.uninstall(handle);
  assert.deepEqual(log.filtered('dispose:'), [
    'dispose:c',
    'dispose:b',
    'dispose:a',
  ]);
  assert.equal(host.isReady('a'), false);
});

test('cleanup failures are collected without blocking later cleanups', async () => {
  const host = new PluginHost();
  const log = new EventLog();
  const handle = await host.install([
    loggingPlugin('a', log, { failDispose: true }),
    loggingPlugin('b', log, { dependencies: ['a'] }),
    loggingPlugin('c', log, { dependencies: ['b'] }),
  ]);
  await assert.rejects(host.uninstall(handle), (error: unknown) => {
    if (!(error instanceof CleanupError)) {
      throw error;
    }
    assert.equal(error.failures.length, 1);
    assert.equal(error.failures[0]?.plugin, 'a');
    return true;
  });
  // All three disposals ran despite 'a' failing.
  assert.deepEqual(log.filtered('dispose:'), [
    'dispose:c',
    'dispose:b',
    'dispose:a',
  ]);
});

test('concurrent installs share an in-flight dependency initialization', async () => {
  const host = new PluginHost();
  const depFactory = new ControllableFactory<{ id: number }>();
  const dep: PluginDefinition = { name: 'dep', factory: depFactory.factory };

  const first = host.install([
    dep,
    { name: 'x', dependencies: ['dep'], factory: () => 'x-instance' },
  ]);
  const second = host.install([
    dep,
    { name: 'y', dependencies: ['dep'], factory: () => 'y-instance' },
  ]);

  await depFactory.waitForCalls(1);
  // The shared dependency is still initializing; only one factory call.
  assert.equal(depFactory.callCount, 1);

  depFactory.lastCall().resolve({ id: 42 });
  const [handleA, handleB] = await Promise.all([first, second]);

  assert.equal(depFactory.callCount, 1);
  assert.equal(host.refCount('dep'), 2);
  assert.equal(host.get<{ id: number }>('dep').id, 42);

  // Releasing one handle keeps the shared dependency alive.
  await host.uninstall(handleA);
  assert.equal(host.refCount('dep'), 1);
  assert.equal(host.isReady('dep'), true);

  await host.uninstall(handleB);
  assert.equal(host.refCount('dep'), 0);
  assert.equal(host.isReady('dep'), false);
});

test('initialization failure propagates to all concurrent installers', async () => {
  const host = new PluginHost();
  const depFactory = new ControllableFactory<never>();
  const dep: PluginDefinition = { name: 'dep', factory: depFactory.factory };

  const first = host.install([dep]);
  const second = host.install([dep]);
  await depFactory.waitForCalls(1);

  const cause = new Error('dep exploded');
  depFactory.lastCall().reject(cause);

  for (const attempt of [first, second]) {
    await assert.rejects(attempt, (error: unknown) => {
      if (!(error instanceof PluginInstallError)) {
        throw error;
      }
      assert.equal(error.plugin, 'dep');
      assert.equal(error.cause, cause);
      return true;
    });
  }
  assert.equal(host.refCount('dep'), 0);
  assert.equal(host.isReady('dep'), false);

  // A later install retries the failed plugin.
  const retry = host.install([dep]);
  await depFactory.waitForCalls(2);
  depFactory.lastCall().resolve(undefined as never);
  await retry;
  assert.equal(host.isReady('dep'), true);
});

test('concurrent failure keeps instances still referenced by others', async () => {
  const host = new PluginHost();
  const log = new EventLog();
  const depFactory = new ControllableFactory<string>();

  const slow = host.install([
    { name: 'dep', factory: depFactory.factory },
    {
      name: 'slow-user',
      dependencies: ['dep'],
      factory: () => {
        log.record('init:slow-user');
        return 'slow';
      },
    },
  ]);
  await depFactory.waitForCalls(1);
  depFactory.lastCall().resolve('shared-dep');

  // Wait until the first install fully commits its references.
  const slowHandle = await slow;

  // A second install acquires 'dep', then fails on its own plugin.
  const failing = host.install([
    { name: 'dep', factory: depFactory.factory },
    {
      name: 'broken',
      dependencies: ['dep'],
      factory: () => Promise.reject(new Error('nope')),
    },
  ]);
  await assert.rejects(failing, PluginInstallError);

  // The shared dependency survives the other install's rollback.
  assert.equal(host.isReady('dep'), true);
  assert.equal(host.refCount('dep'), 1);
  assert.deepEqual(log.filtered('dispose:'), []);

  await host.uninstall(slowHandle);
  assert.equal(host.isReady('dep'), false);
});

test('reference counting across sequential installs', async () => {
  const host = new PluginHost();
  const log = new EventLog();
  const shared = loggingPlugin('shared', log);

  const h1 = await host.install([shared, loggingPlugin('one', log, { dependencies: ['shared'] })]);
  const h2 = await host.install([shared, loggingPlugin('two', log, { dependencies: ['shared'] })]);
  assert.equal(host.refCount('shared'), 2);
  assert.deepEqual(log.filtered('init:shared'), ['init:shared']);

  await host.uninstall(h1);
  assert.deepEqual(log.filtered('dispose:'), ['dispose:one']);
  assert.equal(host.isReady('shared'), true);

  await host.uninstall(h2);
  assert.deepEqual(log.filtered('dispose:'), [
    'dispose:one',
    'dispose:two',
    'dispose:shared',
  ]);
});

test('dispose() cleans up everything in reverse dependency order', async () => {
  const host = new PluginHost();
  const log = new EventLog();
  await host.install([
    loggingPlugin('a', log),
    loggingPlugin('b', log, { dependencies: ['a'] }),
  ]);
  await host.install([loggingPlugin('c', log)]);
  await host.dispose();
  const disposed = log.filtered('dispose:');
  assert.deepEqual([...disposed].sort(), ['dispose:a', 'dispose:b', 'dispose:c']);
  assert.ok(disposed.indexOf('dispose:b') < disposed.indexOf('dispose:a'));
});

test('dispose() collects failures without blocking remaining cleanups', async () => {
  const host = new PluginHost();
  const log = new EventLog();
  await host.install([
    loggingPlugin('a', log, { failDispose: true }),
    loggingPlugin('b', log),
  ]);
  await assert.rejects(host.dispose(), (error: unknown) => {
    if (!(error instanceof CleanupError)) {
      throw error;
    }
    assert.equal(error.failures.length, 1);
    assert.equal(error.failures[0]?.plugin, 'a');
    return true;
  });
  assert.deepEqual([...log.filtered('dispose:')].sort(), [
    'dispose:a',
    'dispose:b',
  ]);
});

test('instance dispose() method is used when no dispose option given', async () => {
  const host = new PluginHost();
  let disposed = 0;
  const handle = await host.install([
    {
      name: 'service',
      factory: () => ({ dispose: () => { disposed += 1; } }),
    },
  ]);
  await host.uninstall(handle);
  assert.equal(disposed, 1);
});

test('detects dependency cycles', async () => {
  const host = new PluginHost();
  await assert.rejects(
    host.install([
      { name: 'a', dependencies: ['b'], factory: () => 'a' },
      { name: 'b', dependencies: ['a'], factory: () => 'b' },
    ]),
    (error: unknown) => {
      if (!(error instanceof PluginCycleError)) {
        throw error;
      }
      assert.deepEqual(error.cycle, ['a', 'b', 'a']);
      return true;
    },
  );
});

test('rejects unknown dependencies', async () => {
  const host = new PluginHost();
  await assert.rejects(
    host.install([
      { name: 'a', dependencies: ['missing'], factory: () => 'a' },
    ]),
    UnknownPluginError,
  );
});

runTests();
