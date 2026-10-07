import { describe, expect, test } from 'bun:test';
import { createControllableStore } from '../src/index';
import type { JournalKind } from '../src/index';

function kinds(store: { getJournal(): readonly { kind: JournalKind }[] }): JournalKind[] {
  return store.getJournal().map((record) => record.kind);
}

describe('受控属性与内部默认值', () => {
  test('非受控：展示内部默认值，输入直接更新展示值', () => {
    const store = createControllableStore<string>({ defaultValue: 'a' });
    expect(store.getSnapshot().controlled).toBe(false);
    expect(store.getSnapshot().value).toBe('a');

    store.input('b');
    const snap = store.getSnapshot();
    expect(snap.value).toBe('b');
    expect(snap.draft).toBe('b');
    expect(snap.version).toBe(1);
    expect(kinds(store)).toEqual(['input-accepted']);
  });

  test('受控：展示以外部值为准，真实输入保留在草稿中', () => {
    const store = createControllableStore<string>({ value: 'ext', defaultValue: 'a' });
    expect(store.getSnapshot().controlled).toBe(true);
    expect(store.getSnapshot().value).toBe('ext');

    store.input('typed');
    const snap = store.getSnapshot();
    expect(snap.value).toBe('ext'); // 展示仍以外部值为准
    expect(snap.draft).toBe('typed'); // 最近真实输入被保留
    expect(snap.version).toBe(1);
  });

  test('受控：外部值下发后展示更新', () => {
    const store = createControllableStore<string>({ value: 'v1', defaultValue: 'a' });
    store.syncExternal('v2');
    expect(store.getSnapshot().value).toBe('v2');
    expect(kinds(store)).toEqual(['external-applied']);
  });

  test('非受控：外部值被忽略并记入日志', () => {
    const store = createControllableStore<string>({ defaultValue: 'a' });
    store.syncExternal('x');
    expect(store.getSnapshot().value).toBe('a');
    expect(kinds(store)).toEqual(['external-ignored-uncontrolled']);
  });
});

describe('切换受控状态', () => {
  test('受控切非受控：展示回退到最近真实输入', () => {
    const store = createControllableStore<string>({ value: 'ext', defaultValue: 'a' });
    store.input('typed-1');
    store.input('typed-2');
    store.setControlled(false);

    const snap = store.getSnapshot();
    expect(snap.value).toBe('typed-2'); // 最近真实输入
    expect(snap.draft).toBe('typed-2');
    expect(kinds(store)).toEqual(['input-accepted', 'input-accepted', 'mode-changed']);
  });

  test('非受控切受控：展示以外部值为准，草稿不丢失', () => {
    const store = createControllableStore<string>({ defaultValue: 'a' });
    store.input('draft-1');
    store.setControlled(true);

    let snap = store.getSnapshot();
    expect(snap.value).toBe('a'); // 外部值（初始默认）为准
    expect(snap.draft).toBe('draft-1'); // 草稿保留

    store.syncExternal('ext');
    snap = store.getSnapshot();
    expect(snap.value).toBe('ext');
    expect(snap.draft).toBe('draft-1');

    // 再切回非受控，最近真实输入恢复展示
    store.setControlled(false);
    expect(store.getSnapshot().value).toBe('draft-1');
  });
});

describe('带版本的外部确认', () => {
  test('确认版本覆盖当前输入版本时被接受', () => {
    const store = createControllableStore<string>({ defaultValue: 'a' });
    store.input('b');
    store.input('c');
    expect(store.getSnapshot().dirty).toBe(true);

    store.confirm(2);
    const snap = store.getSnapshot();
    expect(snap.confirmedVersion).toBe(2);
    expect(snap.dirty).toBe(false);
    expect(kinds(store)).toEqual(['input-accepted', 'input-accepted', 'confirmed']);
  });

  test('旧确认被拒绝，不能覆盖新输入', () => {
    const store = createControllableStore<string>({ defaultValue: 'a' });
    store.input('b'); // version 1
    store.confirm(1); // 接受
    store.input('c'); // version 2
    store.confirm(1); // 旧确认，拒绝

    const snap = store.getSnapshot();
    expect(snap.draft).toBe('c'); // 新输入未被覆盖
    expect(snap.value).toBe('c');
    expect(snap.version).toBe(2);
    expect(snap.confirmedVersion).toBe(1);
    expect(snap.dirty).toBe(true);
    expect(kinds(store)).toEqual([
      'input-accepted',
      'confirmed',
      'input-accepted',
      'confirm-rejected',
    ]);
  });

  test('确认不修改草稿与展示值', () => {
    const store = createControllableStore<string>({ defaultValue: 'a' });
    store.input('b');
    store.confirm(1);
    expect(store.getSnapshot().value).toBe('b');
    expect(store.getSnapshot().draft).toBe('b');
  });
});

describe('撤销', () => {
  test('非受控：撤销回退展示值并递增版本', () => {
    const store = createControllableStore<string>({ defaultValue: 'a' });
    store.input('b');
    store.input('c');
    store.undo();

    const snap = store.getSnapshot();
    expect(snap.value).toBe('b');
    expect(snap.version).toBe(3); // 撤销本身也是一次真实输入
    expect(kinds(store)).toEqual(['input-accepted', 'input-accepted', 'undo-applied']);
  });

  test('受控：撤销回退草稿并通知外部，展示仍以外部值为准', () => {
    const changes: Array<[string, number]> = [];
    const store = createControllableStore<string>({
      value: 'ext',
      defaultValue: 'a',
      onChange: (value, version) => changes.push([value, version]),
    });
    store.input('typed');
    store.undo();

    const snap = store.getSnapshot();
    expect(snap.draft).toBe('a');
    expect(snap.value).toBe('ext'); // 展示不变
    expect(changes).toEqual([
      ['typed', 1],
      ['a', 2],
    ]);
  });

  test('空历史撤销为 no-op', () => {
    const store = createControllableStore<string>({ defaultValue: 'a' });
    store.undo();
    expect(store.getSnapshot().value).toBe('a');
    expect(store.getSnapshot().canUndo).toBe(false);
    expect(kinds(store)).toEqual(['undo-noop']);
  });
});

describe('反馈循环防护', () => {
  test('外部同步事件绝不触发 onChange', () => {
    let calls = 0;
    const store = createControllableStore<string>({
      value: 'v1',
      defaultValue: 'a',
      onChange: () => {
        calls += 1;
      },
    });
    store.syncExternal('v2');
    store.setControlled(false);
    store.setControlled(true);
    store.confirm(0);
    expect(calls).toBe(0);
  });

  test('父级回显相同值被抑制，不重复通知', () => {
    const notified: string[] = [];
    const store = createControllableStore<string>({ value: 'v1', defaultValue: 'a' });
    store.subscribe((value) => notified.push(value));

    store.syncExternal('v2');
    store.syncExternal('v2'); // 回显/重复下发
    expect(notified).toEqual(['v2']);
    expect(kinds(store)).toEqual(['external-applied', 'external-echo-suppressed']);
  });

  test('onChange 中同步回写（受控回路）不会无限递归，日志顺序可核对', () => {
    const notified: string[] = [];
    const store = createControllableStore<string>({
      value: 'v0',
      defaultValue: 'a',
      onChange: (value) => {
        // 模拟父组件：收到 onChange 后把值作为受控值回写
        store.syncExternal(value);
      },
    });
    store.subscribe((value) => notified.push(value));

    store.input('typed');

    const snap = store.getSnapshot();
    expect(snap.externalValue).toBe('typed');
    expect(snap.value).toBe('typed'); // 回写生效，展示与外部值一致
    expect(notified).toEqual(['typed']);
    // 顺序：输入先入账，回写的外部位其后
    expect(kinds(store)).toEqual(['input-accepted', 'external-applied']);
  });

  test('订阅者仅在展示值变化时被通知', () => {
    const notified: string[] = [];
    const store = createControllableStore<string>({ value: 'ext', defaultValue: 'a' });
    store.subscribe((value) => notified.push(value));

    store.input('typed'); // 受控下展示不变，不通知
    expect(notified).toEqual([]);

    store.setControlled(false); // 展示切换到草稿
    expect(notified).toEqual(['typed']);
  });
});
