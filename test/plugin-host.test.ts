import {
  PluginCycleError,
  PluginHost,
  PluginInstallError,
  UnknownPluginError,
} from '../src/plugin-host';
import { ControllableFactory } from './controllable-factory';
import { assert, assertDeepEqual, assertEqual, assertRejects, test } from './runner';

test('initializes plugins in dependency order', async () => {
  const factory = new ControllableFactory();
  const host = new PluginHost();
  host.registerAll([
    factory.define('a', ['b']),
    factory.define('b', ['c']),
    factory.define('c'),
  ]);

  await host.install('a');

  assertDeepEqual(factory.events, [
    'init:c', 'ready:c',
    'init:b', 'ready:b',
    'init:a', 'ready:a',
  ]);
  assertEqual(host.refCount('a'), 1);
  assertEqual(host.refCount('b'), 1);
  assertEqual(host.refCount('c'), 1);
});

test('shares initialized dependencies and cleans up in reverse order', async () => {
  const factory = new ControllableFactory();
  const host = new PluginHost();
  host.registerAll([
    factory.define('p1', ['d']),
    factory.define('p2', ['d']),
    factory.define('d'),
  ]);

  await host.install('p1');
  await host.install('p2');

  assertEqual(factory.initCount('d'), 1);
  assertEqual(host.refCount('d'), 2);
  assert(factory.depsSeen.get('p1')!['d'] === factory.depsSeen.get('p2')!['d']);

  const firstCleanup = await host.uninstall('p1');
  assertDeepEqual(firstCleanup, []);
  assert(host.isReady('d'), 'shared dependency must survive while still referenced');
  assertDeepEqual(factory.disposeEvents(), ['dispose:p1']);

  const secondCleanup = await host.uninstall('p2');
  assertDeepEqual(secondCleanup, []);
  assertDeepEqual(factory.disposeEvents(), ['dispose:p1', 'dispose:p2', 'dispose:d']);
  assert(!host.isReady('d'));
});

test('rolls back only instances created by the failed install', async () => {
  const factory = new ControllableFactory();
  const host = new PluginHost();
  host.registerAll([
    factory.define('s'),
    factory.define('x', ['s']),
    factory.define('bad'),
    factory.define('y', ['x', 'bad']),
  ]);

  await host.install('s');
  assertEqual(factory.initCount('s'), 1);

  const boom = new Error('boom');
  factory.failInit('bad', boom);

  const error = await assertRejects(() => host.install('y'));
  assert(error instanceof PluginInstallError);
  assertEqual(error.plugin, 'y');
  assertEqual(error.cause, boom);
  assertDeepEqual(error.rollbackFailures, []);

  assert(host.isReady('s'), 'pre-existing shared dependency must be kept');
  assertEqual(factory.initCount('s'), 1, 'shared dependency must not be re-initialized');
  assert(!host.isReady('x'), 'newly created dependency must be rolled back');
  assert(!host.isReady('bad'));
  assert(!host.isReady('y'));
  assertDeepEqual(factory.disposeEvents(), ['dispose:x']);
});

test('collects rollback cleanup failures and still propagates the original error', async () => {
  const factory = new ControllableFactory();
  const host = new PluginHost();
  host.registerAll([
    factory.define('x'),
    factory.define('bad'),
    factory.define('y', ['x', 'bad']),
  ]);

  const boom = new Error('init boom');
  const cleanupBoom = new Error('cleanup boom');
  factory.failInit('bad', boom);
  factory.failDispose('x', cleanupBoom);

  const error = await assertRejects(() => host.install('y'));
  assert(error instanceof PluginInstallError);
  assertEqual(error.cause, boom);
  assertEqual(error.rollbackFailures.length, 1);
  assertEqual(error.rollbackFailures[0]!.plugin, 'x');
  assertEqual(error.rollbackFailures[0]!.error, cleanupBoom);
  assertDeepEqual(factory.disposeEvents(), ['dispose:x']);
});

test('shares an in-flight dependency between concurrent installs', async () => {
  const factory = new ControllableFactory();
  const host = new PluginHost();
  host.registerAll([
    factory.define('p1', ['d']),
    factory.define('p2', ['d']),
    factory.define('d'),
  ]);

  factory.gate('d');
  const first = host.install('p1');
  const second = host.install('p2');

  assertEqual(factory.initCount('d'), 1, 'dependency factory must run exactly once');

  factory.release('d');
  await Promise.all([first, second]);

  assertEqual(host.refCount('d'), 2);
  assert(factory.depsSeen.get('p1')!['d'] === factory.depsSeen.get('p2')!['d']);
  assertEqual(host.getInstance('d'), factory.depsSeen.get('p1')!['d']);
});

test('propagates initialization failure to all concurrent installers', async () => {
  const factory = new ControllableFactory();
  const host = new PluginHost();
  host.registerAll([
    factory.define('p1', ['d']),
    factory.define('p2', ['d']),
    factory.define('d'),
  ]);

  factory.gate('d');
  const first = host.install('p1');
  const second = host.install('p2');
  assertEqual(factory.initCount('d'), 1);

  const boom = new Error('dependency failed');
  factory.rejectGate('d', boom);

  const firstError = await assertRejects(() => first);
  const secondError = await assertRejects(() => second);
  assert(firstError instanceof PluginInstallError);
  assert(secondError instanceof PluginInstallError);
  assertEqual(firstError.cause, boom);
  assertEqual(secondError.cause, boom);
  assert(!host.isReady('d'), 'failed dependency must be evicted');

  await host.install('p1');
  assertEqual(factory.initCount('d'), 2, 'dependency is retried after failure');
  assert(host.isReady('d'));
});

test('collects cleanup failures without blocking remaining cleanup', async () => {
  const factory = new ControllableFactory();
  const host = new PluginHost();
  host.registerAll([
    factory.define('a', ['b']),
    factory.define('b', ['c']),
    factory.define('c'),
  ]);

  await host.install('a');

  const cleanupBoom = new Error('dispose boom');
  factory.failDispose('b', cleanupBoom);

  const failures = await host.uninstall('a');
  assertEqual(failures.length, 1);
  assertEqual(failures[0]!.plugin, 'b');
  assertEqual(failures[0]!.error, cleanupBoom);
  assertDeepEqual(factory.disposeEvents(), ['dispose:a', 'dispose:b', 'dispose:c']);
  assert(!host.isReady('a') && !host.isReady('b') && !host.isReady('c'));
});

test('shutdown disposes all plugins in reverse dependency order', async () => {
  const factory = new ControllableFactory();
  const host = new PluginHost();
  host.registerAll([
    factory.define('p1', ['d']),
    factory.define('p2', ['d']),
    factory.define('d'),
  ]);

  await host.install('p1');
  await host.install('p2');

  const cleanupBoom = new Error('dispose boom');
  factory.failDispose('p1', cleanupBoom);

  const failures = await host.shutdown();
  assertEqual(failures.length, 1);
  assertEqual(failures[0]!.plugin, 'p1');
  assertDeepEqual(factory.disposeEvents(), ['dispose:p1', 'dispose:p2', 'dispose:d']);
  assert(!host.isReady('p1') && !host.isReady('p2') && !host.isReady('d'));
});

test('detects circular dependencies', async () => {
  const factory = new ControllableFactory();
  const host = new PluginHost();
  host.registerAll([
    factory.define('a', ['b']),
    factory.define('b', ['a']),
  ]);

  const error = await assertRejects(() => host.install('a'));
  assert(error instanceof PluginInstallError);
  assert(error.cause instanceof PluginCycleError);
});

test('reference counts root installs', async () => {
  const factory = new ControllableFactory();
  const host = new PluginHost();
  host.registerAll([factory.define('a')]);

  await host.install('a');
  await host.install('a');
  assertEqual(host.refCount('a'), 2);
  assertEqual(factory.initCount('a'), 1);

  await host.uninstall('a');
  assert(host.isReady('a'), 'instance survives until the last uninstall');
  await host.uninstall('a');
  assert(!host.isReady('a'));
  assertDeepEqual(factory.disposeEvents(), ['dispose:a']);
});

test('rejects unknown plugins and uninstalls of non-installed plugins', async () => {
  const host = new PluginHost();

  const installError = await assertRejects(() => host.install('nope'));
  assert(installError instanceof PluginInstallError);
  assert(installError.cause instanceof UnknownPluginError);

  await assertRejects(() => host.uninstall('nope'));
});
