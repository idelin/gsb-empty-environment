import assert from 'node:assert/strict';
import test from 'node:test';
import { CommandCenter } from '../src/command-center.ts';
import {
  CommandNotFoundError,
  CommandUnavailableError,
} from '../src/registry.ts';

const noop = (): undefined => undefined;

test('排序：用户优先级降序，再按 id 升序', () => {
  const center = new CommandCenter();
  center.register({ id: 'beta', handler: noop, priority: 10 });
  center.register({ id: 'alpha', handler: noop, priority: 10 });
  center.register({ id: 'gamma', handler: noop, priority: 20 });
  center.register({ id: 'delta', handler: noop });
  center.register({ id: 'zeta', handler: noop, priority: -5 });
  const { commands } = center.list();
  assert.deepEqual(
    commands.map((c) => c.id),
    ['gamma', 'alpha', 'beta', 'delta', 'zeta'],
  );
});

test('when 条件过滤可用命令', () => {
  const center = new CommandCenter();
  center.register({ id: 'save', handler: noop, when: 'focus.editor && !readonly' });
  center.register({ id: 'refresh', handler: noop });
  center.focus('editor');
  // 同优先级按 id 升序：refresh < save
  assert.deepEqual(center.list().commands.map((c) => c.id), ['refresh', 'save']);
  center.setContext('readonly', true);
  assert.deepEqual(center.list().commands.map((c) => c.id), ['refresh']);
});

test('list 快照携带上下文版本，上下文变更使版本递增', () => {
  const center = new CommandCenter();
  center.register({ id: 'save', handler: noop, when: 'focus.editor' });
  center.focus('editor');
  const first = center.list();
  assert.equal(first.version, center.context.version);
  center.setContext('dirty', true);
  const second = center.list();
  assert.ok(second.version > first.version);
  // 设置相同的值不 bump 版本
  center.setContext('dirty', true);
  assert.equal(center.context.version, second.version);
});

test('执行时按当前上下文重新求值：列表可用不保证稍后仍可执行', () => {
  const center = new CommandCenter();
  let ran = 0;
  center.register({ id: 'save', handler: () => ran++, when: 'focus.editor' });
  center.focus('editor');
  const snapshot = center.list();
  assert.deepEqual(snapshot.commands.map((c) => c.id), ['save']);
  // 快照之后焦点切走，可用性保证失效
  center.focus('terminal');
  assert.throws(
    () => center.execute('save', [], snapshot.version),
    (err: unknown) => err instanceof CommandUnavailableError,
  );
  assert.equal(ran, 0);
  const rejected = center.log.ofType('command.executeRejected');
  assert.equal(rejected.length, 1);
  assert.equal(rejected[0].reason, 'unavailable');
  assert.equal(rejected[0].revalidated, true);
});

test('版本未变且条件仍满足时正常执行', () => {
  const center = new CommandCenter();
  center.register({ id: 'save', handler: (x: unknown) => `saved:${x}`, when: 'focus.editor' });
  center.focus('editor');
  const snapshot = center.list();
  assert.equal(center.execute('save', ['file.ts'], snapshot.version), 'saved:file.ts');
  const executed = center.log.ofType('command.executed');
  assert.equal(executed.length, 1);
  assert.equal(executed[0].revalidated, false);
});

test('版本过期但条件仍满足：重校验后放行并标记 revalidated', () => {
  const center = new CommandCenter();
  let ran = 0;
  center.register({ id: 'save', handler: () => { ran += 1; }, when: 'focus.editor' });
  center.focus('editor');
  const snapshot = center.list();
  center.setContext('unrelated', 1); // 只 bump 版本，不影响条件
  center.execute('save', [], snapshot.version);
  assert.equal(ran, 1);
  const executed = center.log.ofType('command.executed');
  assert.equal(executed[0].revalidated, true);
});

test('执行已删除的命令抛出 CommandNotFoundError', () => {
  const center = new CommandCenter();
  center.register({ id: 'save', handler: noop });
  const snapshot = center.list();
  assert.equal(center.unregister('save'), true);
  assert.throws(
    () => center.execute('save', [], snapshot.version),
    (err: unknown) => err instanceof CommandNotFoundError,
  );
  const rejected = center.log.ofType('command.executeRejected');
  assert.equal(rejected[0].reason, 'not-found');
});
