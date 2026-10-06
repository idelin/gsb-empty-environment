import assert from 'node:assert/strict';
import test from 'node:test';
import { CommandCenter } from '../src/command-center.ts';
import { ConditionParseError } from '../src/expression.ts';
import { replay } from '../src/replay.ts';
import { CommandNotFoundError } from '../src/registry.ts';

const noop = (): undefined => undefined;

const availableIds = (center: CommandCenter): string[] =>
  center.list().commands.map((c) => c.id);

test('重放：焦点切换改变可用命令集合', () => {
  const center = new CommandCenter();
  center.register({ id: 'save', handler: noop, priority: 10, when: 'focus.editor && !readonly' });
  center.register({ id: 'format', handler: noop, priority: 5, when: 'focus.editor' });
  center.register({ id: 'refresh', handler: noop });

  center.focus('editor');
  assert.deepEqual(availableIds(center), ['save', 'format', 'refresh']);
  center.focus('terminal');
  assert.deepEqual(availableIds(center), ['refresh']);
  center.focus('editor');
  center.setContext('readonly', true);
  assert.deepEqual(availableIds(center), ['format', 'refresh']);

  const { center: replayed, errors } = replay(center.log.events);
  assert.deepEqual(errors, []);
  assert.deepEqual(availableIds(replayed), ['format', 'refresh']);
  assert.equal(replayed.context.version, center.context.version);
  assert.deepEqual(replayed.context.snapshot(), center.context.snapshot());
  assert.equal(replayed.focusManager.current, 'editor');
});

test('重放：命令删除后执行失败，重放后同样失败', () => {
  const center = new CommandCenter();
  center.register({ id: 'save', handler: noop, priority: 1 });
  center.register({ id: 'open', handler: noop, priority: 2 });
  const snapshot = center.list();
  assert.deepEqual(snapshot.commands.map((c) => c.id), ['open', 'save']);

  center.unregister('save');
  assert.throws(() => center.execute('save', [], snapshot.version), CommandNotFoundError);
  assert.deepEqual(availableIds(center), ['open']);

  const { center: replayed, errors } = replay(center.log.events);
  assert.deepEqual(errors, []);
  assert.equal(replayed.registry.has('save'), false);
  assert.throws(() => replayed.execute('save'), CommandNotFoundError);
  assert.deepEqual(availableIds(replayed), ['open']);
});

test('重放：条件解析错误被记录且命令不进入注册表', () => {
  const center = new CommandCenter();
  assert.throws(
    () => center.register({ id: 'broken', handler: noop, when: 'focus.editor &&' }),
    (err: unknown) => err instanceof ConditionParseError,
  );
  center.register({ id: 'ok', handler: noop, when: 'focus.editor' });

  const parseErrors = center.log.ofType('condition.parseError');
  assert.equal(parseErrors.length, 1);
  assert.equal(parseErrors[0].id, 'broken');
  assert.equal(parseErrors[0].when, 'focus.editor &&');
  assert.equal(parseErrors[0].offset, 'focus.editor &&'.length);
  assert.match(parseErrors[0].message, /unexpected end of expression/);
  assert.equal(center.registry.has('broken'), false);

  // 解析失败的注册从未产生 command.registered 事件，重放天然一致
  const { center: replayed, errors } = replay(center.log.events);
  assert.deepEqual(errors, []);
  assert.equal(replayed.registry.has('broken'), false);
  assert.equal(replayed.registry.has('ok'), true);
});

test('重放：完整事件流端到端确定重建', () => {
  const center = new CommandCenter();
  center.register({ id: 'save', handler: noop, priority: 10, when: 'focus.editor && !readonly' });
  center.register({ id: 'terminal.clear', handler: noop, priority: 10, when: "focus == 'terminal'" });
  center.register({ id: 'refresh', handler: noop, priority: 1 });
  center.focus('editor');
  center.execute('save');
  center.focus('terminal');
  center.execute('terminal.clear');
  center.unregister('refresh');
  center.blur();

  const { center: replayed, errors } = replay(center.log.events);
  assert.deepEqual(errors, []);
  assert.deepEqual(availableIds(replayed), availableIds(center));
  assert.equal(replayed.context.version, center.context.version);
  assert.deepEqual(replayed.context.snapshot(), center.context.snapshot());
  assert.equal(replayed.focusManager.current, null);
});
