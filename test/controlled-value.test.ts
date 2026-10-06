import assert from 'node:assert/strict';
import test from 'node:test';
import { ControlledValueStore } from '../src/index.ts';

test('非受控：默认值起步，真实输入直接成为展示值并推进版本', () => {
  const store = new ControlledValueStore<string>({ defaultValue: 'init' });
  assert.equal(store.getValue(), 'init');
  assert.equal(store.getState().controlled, false);

  store.input('a');
  assert.equal(store.getValue(), 'a');
  assert.equal(store.getState().inputVersion, 1);

  store.input('b');
  assert.equal(store.getValue(), 'b');
  assert.equal(store.getState().inputVersion, 2);
});

test('受控：展示以外部值为准，真实输入只沉淀为草稿', () => {
  const store = new ControlledValueStore<string>({ defaultValue: 'init', value: 'EXT' });
  assert.equal(store.getValue(), 'EXT');

  store.input('typed');
  assert.equal(store.getValue(), 'EXT', '受控时展示仍以外部值为准');
  assert.equal(store.getState().draft, 'typed', '最近真实输入被保留');
  assert.equal(store.getState().inputVersion, 1);
});

test('切换：切入受控展示外部值，切出后恢复最近真实输入', () => {
  const store = new ControlledValueStore<string>({ defaultValue: 'init' });
  store.input('a');
  store.input('b');

  store.syncExternal('X');
  store.setControlled(true);
  assert.equal(store.getValue(), 'X', '受控时外部值优先展示');
  assert.equal(store.getState().draft, 'b', '草稿不受切换影响');

  store.input('c');
  assert.equal(store.getValue(), 'X');

  store.setControlled(false);
  assert.equal(store.getValue(), 'c', '切回非受控恢复最近真实输入');
});

test('回显：外部同步不推进版本、不污染草稿，阻断反馈循环', () => {
  const store = new ControlledValueStore<string>({ defaultValue: '', value: '' });
  store.input('hello');
  const versionBefore = store.getState().inputVersion;

  // 输入经外部往返后的回显
  store.syncExternal('hello');
  assert.equal(store.getState().inputVersion, versionBefore, '回显不产生新版本');
  assert.equal(store.getState().draft, 'hello', '回显不覆盖草稿');

  // 外部真正下发新值：展示跟随，草稿仍保留
  store.syncExternal('server');
  assert.equal(store.getValue(), 'server');
  assert.equal(store.getState().draft, 'hello');
  assert.equal(store.getState().inputVersion, versionBefore);
});

test('确认：过期确认被拒绝，不得覆盖更新的输入；当期确认被接受', () => {
  const store = new ControlledValueStore<string>({ defaultValue: '' });
  store.input('a'); // version 1
  store.input('b'); // version 2

  const stale = store.confirm('a', 1);
  assert.equal(stale.applied, false);
  assert.equal(stale.reason, 'stale-confirmation');
  assert.equal(store.getState().draft, 'b', '旧确认不能覆盖新输入');
  assert.equal(store.getState().confirmedVersion, 0);

  const fresh = store.confirm('b', 2);
  assert.equal(fresh.applied, true);
  assert.equal(store.getState().draft, 'b');
  assert.equal(store.getState().confirmedVersion, 2);
});

test('撤销：逐步回退真实输入，历史耗尽后拒绝', () => {
  const store = new ControlledValueStore<string>({ defaultValue: 'init' });
  store.input('a');
  store.input('b');
  store.input('c');

  store.undo();
  assert.equal(store.getValue(), 'b');
  assert.equal(store.getState().inputVersion, 2);

  store.undo();
  assert.equal(store.getValue(), 'a');

  store.undo();
  assert.equal(store.getValue(), 'init');
  assert.equal(store.getState().inputVersion, 0);

  const empty = store.undo();
  assert.equal(empty.applied, false);
  assert.equal(empty.reason, 'empty-history');
});

test('撤销与确认协作：撤销回退版本后，此前的确认不被误判为过期', () => {
  const store = new ControlledValueStore<string>({ defaultValue: '' });
  store.input('a'); // v1
  store.input('b'); // v2
  store.undo(); // 回到 v1，草稿 'a'

  const result = store.confirm('a', 1);
  assert.equal(result.applied, true, '版本与当前输入持平的确认应被接受');
  assert.equal(store.getState().confirmedVersion, 1);
});

test('通知：仅展示值变化时触发，订阅者回写外部值不会死循环', () => {
  const store = new ControlledValueStore<string>({ defaultValue: '', value: '' });
  const seen: string[] = [];
  store.subscribe((value) => {
    seen.push(value);
    // 模拟受控组件把展示值回写为外部值（典型 echo 回路）
    store.syncExternal(value);
  });

  store.input('typed'); // 受控下输入不改变展示值
  assert.deepEqual(seen, [], '受控下输入不触发通知');

  store.syncExternal('next'); // 外部值真正变化
  assert.deepEqual(seen, ['next'], '只通知一次，回写因等值被拒绝而终止');

  const noop = store.getLog().at(-1);
  assert.equal(noop?.applied, false);
  assert.equal(noop?.reason, 'no-op');
});

test('事件日志：完整记录应用与拒绝，可逐一核对', () => {
  const store = new ControlledValueStore<string>({ defaultValue: '' });
  store.input('x');
  store.confirm('x', 0); // 过期：当前版本已是 1
  store.setControlled(false); // 重复切换
  store.undo();
  store.undo(); // 历史为空

  const log = store.getLog();
  assert.deepEqual(
    log.map((entry) => [entry.event.type, entry.applied, entry.reason ?? null]),
    [
      ['input', true, null],
      ['confirm', false, 'stale-confirmation'],
      ['set-controlled', false, 'no-op'],
      ['undo', true, null],
      ['undo', false, 'empty-history'],
    ],
  );
  assert.deepEqual(log.map((entry) => entry.seq), [0, 1, 2, 3, 4]);
});
