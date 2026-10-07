import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CommandNotFoundError,
  CommandRegistry,
  CommandUnavailableError,
  DuplicateCommandError,
  StaleContextError,
} from '../src/index.ts';

test('可用命令按用户优先级降序、id 升序排序', () => {
  const registry = new CommandRegistry();
  registry.register({ id: 'gamma', priority: 5 });
  registry.register({ id: 'alpha', priority: 10 });
  registry.register({ id: 'delta', priority: 5 });
  registry.register({ id: 'beta' });

  const list = registry.listAvailable();
  assert.deepEqual(list.commands.map((c) => c.id), ['alpha', 'delta', 'gamma', 'beta']);
  assert.equal(list.contextVersion, 0);
  assert.deepEqual(list.errors, []);
});

test('when 条件过滤可用命令，默认 when 为 true', () => {
  const registry = new CommandRegistry();
  registry.register({ id: 'always' });
  registry.register({ id: 'focused-only', when: 'editorFocus' });
  registry.setContext('editorFocus', false);

  assert.deepEqual(registry.listAvailable().commands.map((c) => c.id), ['always']);
  registry.setContext('editorFocus', true);
  assert.deepEqual(registry.listAvailable().commands.map((c) => c.id), ['always', 'focused-only']);
});

test('上下文每次实际变更都会递增版本号', () => {
  const registry = new CommandRegistry();
  assert.equal(registry.contextVersion, 0);
  registry.setContext('a', true);
  registry.setContext('a', true);
  assert.equal(registry.contextVersion, 1);
  registry.setContext('a', false);
  registry.unsetContext('a');
  registry.unsetContext('missing');
  assert.equal(registry.contextVersion, 3);
});

test('执行时重新检查上下文版本：列表快照过期则拒绝执行', () => {
  const registry = new CommandRegistry();
  registry.register({ id: 'save', run: () => 'saved' });
  const listed = registry.listAvailable();

  assert.equal(registry.execute('save', { expectedContextVersion: listed.contextVersion }), 'saved');

  registry.setContext('editorFocus', true);
  assert.throws(
    () => registry.execute('save', { expectedContextVersion: listed.contextVersion }),
    (error: unknown) =>
      error instanceof StaleContextError &&
      error.expectedVersion === listed.contextVersion &&
      error.actualVersion === registry.contextVersion,
  );
});

test('列表显示可用不保证稍后仍可执行：执行时按当前上下文重新求值', () => {
  const registry = new CommandRegistry();
  registry.register({ id: 'save', when: 'editorFocus' });
  registry.setContext('editorFocus', true);
  assert.deepEqual(registry.listAvailable().commands.map((c) => c.id), ['save']);

  registry.setContext('editorFocus', false);
  assert.throws(() => registry.execute('save'), CommandUnavailableError);
});

test('执行未注册或已删除的命令抛出 CommandNotFoundError', () => {
  const registry = new CommandRegistry();
  assert.throws(() => registry.execute('nope'), CommandNotFoundError);
  registry.register({ id: 'temp' });
  registry.unregister('temp');
  assert.throws(() => registry.execute('temp'), CommandNotFoundError);
  assert.throws(() => registry.unregister('temp'), CommandNotFoundError);
});

test('重复注册抛出 DuplicateCommandError', () => {
  const registry = new CommandRegistry();
  registry.register({ id: 'save' });
  assert.throws(() => registry.register({ id: 'save' }), DuplicateCommandError);
});

test('handler 接收参数并返回结果', () => {
  const registry = new CommandRegistry();
  registry.register({ id: 'echo', run: (args) => ({ echoed: args }) });
  assert.deepEqual(registry.execute('echo', { args: 42 }), { echoed: 42 });
});
