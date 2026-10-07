import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CommandNotFoundError,
  CommandRegistry,
  ContextResolutionError,
  ExpressionSyntaxError,
  StaleContextError,
} from '../src/index.ts';
import type { RegistryEvent } from '../src/index.ts';

test('事件重放验证焦点切换：可用性随 editorFocus 事件逐步翻转', () => {
  const events: RegistryEvent[] = [
    { type: 'command-registered', id: 'editor.save', priority: 10, when: 'editorFocus && !readonly' },
    { type: 'context-set', key: 'editorFocus', value: true },
    { type: 'context-set', key: 'readonly', value: false },
    { type: 'context-set', key: 'editorFocus', value: false },
    { type: 'context-set', key: 'editorFocus', value: true },
  ];
  const expectedAvailability = [false, false, true, false, true];

  const seen: boolean[] = [];
  const registry = CommandRegistry.replay(events, {
    onStep: (_event, reg) => {
      seen.push(reg.listAvailable().commands.some((c) => c.id === 'editor.save'));
    },
  });

  assert.deepEqual(seen, expectedAvailability);
  assert.equal(registry.contextVersion, 4);
  assert.deepEqual(registry.events, events);
});

test('事件重放验证命令删除：删除后不可列出也不可执行', () => {
  const events: RegistryEvent[] = [
    { type: 'command-registered', id: 'alpha', priority: 1, when: 'true' },
    { type: 'command-registered', id: 'beta', priority: 5, when: 'true' },
    { type: 'command-registered', id: 'gamma', priority: 5, when: 'true' },
    { type: 'command-unregistered', id: 'beta' },
  ];

  const registry = CommandRegistry.replay(events);
  assert.deepEqual(registry.listAvailable().commands.map((c) => c.id), ['gamma', 'alpha']);
  assert.throws(() => registry.execute('beta'), CommandNotFoundError);
  assert.equal(registry.execute('gamma'), undefined);
});

test('事件重放验证条件解析错误：未知键被记录为错误而非崩溃，补齐键后恢复', () => {
  const events: RegistryEvent[] = [
    { type: 'command-registered', id: 'deploy', priority: 0, when: 'isProd && approved' },
    { type: 'context-set', key: 'isProd', value: true },
  ];

  const registry = CommandRegistry.replay(events);
  const list = registry.listAvailable();
  assert.deepEqual(list.commands, []);
  assert.equal(list.errors.length, 1);
  assert.ok(list.errors[0].error instanceof ContextResolutionError);
  assert.equal(list.errors[0].error.commandId, 'deploy');
  assert.equal(list.errors[0].error.key, 'approved');
  assert.throws(
    () => registry.execute('deploy'),
    (error: unknown) => error instanceof ContextResolutionError && error.key === 'approved',
  );

  const repaired = CommandRegistry.replay([
    ...events,
    { type: 'context-set', key: 'approved', value: true },
  ]);
  assert.deepEqual(repaired.listAvailable().commands.map((c) => c.id), ['deploy']);
  assert.deepEqual(repaired.listAvailable().errors, []);
});

test('无效条件表达式在注册时即被拒绝，不会进入事件日志', () => {
  const registry = new CommandRegistry();
  assert.throws(() => registry.register({ id: 'bad', when: '&& oops' }), ExpressionSyntaxError);
  assert.deepEqual(registry.events, []);
  assert.deepEqual(registry.listAvailable().commands, []);
});

test('实时操作与事件重放产生一致的状态快照', () => {
  const live = new CommandRegistry();
  live.register({ id: 'open', priority: 3, when: 'workspaceReady' });
  live.register({ id: 'close', priority: 3, when: 'workspaceReady && !dirty' });
  live.setContext('workspaceReady', true);
  live.setContext('dirty', true);
  live.unregister('open');
  live.setContext('dirty', false);

  const replayed = CommandRegistry.replay(live.events);
  assert.deepEqual(replayed.snapshot(), live.snapshot());
  assert.deepEqual(replayed.listAvailable(), live.listAvailable());
});

test('同一事件日志重放两次结果确定一致', () => {
  const events: RegistryEvent[] = [
    { type: 'command-registered', id: 'a', priority: 0, when: 'x || y' },
    { type: 'context-set', key: 'x', value: false },
    { type: 'context-set', key: 'y', value: true },
    { type: 'context-unset', key: 'y' },
  ];
  const first = CommandRegistry.replay(events);
  const second = CommandRegistry.replay(events);
  assert.deepEqual(first.snapshot(), second.snapshot());
  assert.deepEqual(first.listAvailable(), second.listAvailable());
});

test('重放后的过期版本在执行时仍被拒绝', () => {
  const events: RegistryEvent[] = [
    { type: 'command-registered', id: 'save', priority: 0, when: 'true' },
    { type: 'context-set', key: 'editorFocus', value: true },
  ];
  const registry = CommandRegistry.replay(events, { handlers: { save: () => 'saved' } });
  const listedVersion = registry.listAvailable().contextVersion;

  registry.setContext('editorFocus', false);
  assert.throws(
    () => registry.execute('save', { expectedContextVersion: listedVersion }),
    StaleContextError,
  );
  assert.equal(registry.execute('save', { expectedContextVersion: registry.contextVersion }), 'saved');
});
