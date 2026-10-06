import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  KeymapDispatcher,
  type DispatcherEvent,
  type KeymapConfig,
} from '../src/dispatcher.ts';

const TIMEOUT = 1000;

function makeDispatcher(config: KeymapConfig) {
  const events: DispatcherEvent[] = [];
  const dispatcher = new KeymapDispatcher(config, (event) => events.push(event));
  const executed = () =>
    events.filter((e) => e.type === 'execute').map((e) => (e as { command: string }).command);
  return { dispatcher, events, executed };
}

const baseConfig: KeymapConfig = {
  timeoutMs: TIMEOUT,
  bindings: [
    { keys: ['g'], command: 'short' },
    { keys: ['g', 'g'], command: 'long' },
    { keys: ['x'], command: 'x-cmd' },
  ],
};

test('无更长序列时短命令立即执行', () => {
  const { dispatcher, executed } = makeDispatcher({
    timeoutMs: TIMEOUT,
    bindings: [{ keys: ['x'], command: 'x-cmd' }],
  });
  dispatcher.handleKey('x', 0);
  assert.deepEqual(executed(), ['x-cmd']);
  assert.equal(dispatcher.getState().typed.length, 0);
});

test('前缀嵌套：等待期内不执行，恰好到期执行短命令', () => {
  const { dispatcher, events, executed } = makeDispatcher(baseConfig);
  dispatcher.handleKey('g', 0);
  assert.deepEqual(executed(), []);
  const pending = events.find((e) => e.type === 'pending');
  assert.ok(pending && pending.type === 'pending');
  assert.equal(pending.deadline, TIMEOUT);

  dispatcher.tick(TIMEOUT - 1);
  assert.deepEqual(executed(), []);

  // 恰好到期（at === deadline）即触发短命令
  dispatcher.tick(TIMEOUT);
  assert.deepEqual(executed(), ['short']);
  assert.equal(dispatcher.getState().pendingCommand, null);
});

test('前缀嵌套：等待期内按下后续键立即执行长命令', () => {
  const { dispatcher, executed } = makeDispatcher(baseConfig);
  dispatcher.handleKey('g', 0);
  dispatcher.handleKey('g', TIMEOUT - 1);
  assert.deepEqual(executed(), ['long']);
  // 序列已完结，之后到期不应再有任何触发
  dispatcher.tick(TIMEOUT * 5);
  assert.deepEqual(executed(), ['long']);
});

test('同刻按键：多个相同时间戳的键都在等待窗口内', () => {
  const { dispatcher, executed } = makeDispatcher(baseConfig);
  dispatcher.handleKey('g', 500);
  dispatcher.handleKey('g', 500);
  assert.deepEqual(executed(), ['long']);
});

test('恰好到期时刻到达的键：先超时执行短命令，当前键开启新序列', () => {
  const { dispatcher, executed } = makeDispatcher(baseConfig);
  dispatcher.handleKey('g', 0);
  // 新键恰好到达期限时刻：视为已超时，短命令先执行
  dispatcher.handleKey('x', TIMEOUT);
  assert.deepEqual(executed(), ['short', 'x-cmd']);
});

test('失配：先执行待定短命令，再把当前键重新匹配', () => {
  const { dispatcher, events, executed } = makeDispatcher(baseConfig);
  dispatcher.handleKey('g', 0);
  dispatcher.handleKey('x', 100); // 'g','x' 无绑定 → 执行 short，x 重新匹配命中
  assert.deepEqual(executed(), ['short', 'x-cmd']);
  // 两个命令属于不同代际
  const gens = events.filter((e) => e.type === 'execute').map((e) => e.generation);
  assert.deepEqual(gens, [1, 2]);
});

test('失配且当前键也不匹配：执行短命令后丢弃当前键', () => {
  const { dispatcher, executed } = makeDispatcher(baseConfig);
  dispatcher.handleKey('g', 0);
  dispatcher.handleKey('z', 100);
  assert.deepEqual(executed(), ['short']);
  assert.equal(dispatcher.getState().typed.length, 0);
});

test('多级前缀嵌套：超时执行当前最深的待定短命令', () => {
  const { dispatcher, executed } = makeDispatcher({
    timeoutMs: TIMEOUT,
    bindings: [
      { keys: ['a'], command: 'a' },
      { keys: ['a', 'b'], command: 'ab' },
      { keys: ['a', 'b', 'c'], command: 'abc' },
    ],
  });
  dispatcher.handleKey('a', 0);
  dispatcher.handleKey('b', 100);
  assert.deepEqual(executed(), []);
  dispatcher.tick(100 + TIMEOUT); // 第二级期限恰好到期
  assert.deepEqual(executed(), ['ab']);
});

test('焦点变更取消待定序列', () => {
  const { dispatcher, events, executed } = makeDispatcher(baseConfig);
  dispatcher.handleKey('g', 0);
  dispatcher.focusChanged();
  dispatcher.tick(TIMEOUT * 10);
  assert.deepEqual(executed(), []);
  assert.ok(events.some((e) => e.type === 'cancel' && e.reason === 'focus'));
  // 取消后按键重新开始
  dispatcher.handleKey('g', TIMEOUT * 10);
  dispatcher.handleKey('g', TIMEOUT * 10);
  assert.deepEqual(executed(), ['long']);
});

test('上下文栈：弹层序列遮蔽全局序列', () => {
  const { dispatcher, executed } = makeDispatcher({
    timeoutMs: TIMEOUT,
    bindings: [
      { keys: ['g'], command: 'global-short' },
      { keys: ['g', 'g'], command: 'global-long' },
      { keys: ['g'], command: 'modal-g', context: 'modal' },
    ],
  });
  // 无弹层：走全局前缀等待
  dispatcher.handleKey('g', 0);
  assert.deepEqual(executed(), []);
  dispatcher.focusChanged();

  // 弹层打开：'g' 被弹层独占（无更长序列），立即执行
  dispatcher.pushContext('modal');
  dispatcher.handleKey('g', 10);
  assert.deepEqual(executed(), ['modal-g']);

  // 弹层关闭后恢复全局行为
  dispatcher.popContext();
  dispatcher.handleKey('g', 20);
  dispatcher.handleKey('g', 30);
  assert.deepEqual(executed(), ['modal-g', 'global-long']);
});

test('等待期间上下文变化：重新裁决已输入键，不重复触发命令', () => {
  const { dispatcher, events, executed } = makeDispatcher({
    timeoutMs: TIMEOUT,
    bindings: [
      { keys: ['g'], command: 'global-short' },
      { keys: ['g', 'g'], command: 'global-long' },
      { keys: ['g', 'h'], command: 'modal-gh', context: 'modal' },
    ],
  });
  dispatcher.handleKey('g', 0); // 全局待定 short
  dispatcher.pushContext('modal'); // 重新裁决：弹层 'g h' 遮蔽全局，待定短命令被移除
  assert.deepEqual(executed(), []); // 重新裁决本身不触发任何命令
  assert.equal(dispatcher.getState().pendingCommand, null);

  dispatcher.tick(TIMEOUT); // 原期限到达：没有待定短命令，仅清空
  assert.deepEqual(executed(), []);
  assert.ok(events.some((e) => e.type === 'cancel' && e.reason === 'timeout'));

  // 弹层下完成 'g h'
  dispatcher.handleKey('g', TIMEOUT);
  dispatcher.handleKey('h', TIMEOUT + 1);
  assert.deepEqual(executed(), ['modal-gh']);
});

test('等待期间上下文变化：新上下文的短命令接替待定，期限不变且只触发一次', () => {
  const { dispatcher, executed } = makeDispatcher({
    timeoutMs: TIMEOUT,
    bindings: [
      { keys: ['g'], command: 'global-short' },
      { keys: ['g', 'g'], command: 'global-long' },
      { keys: ['g'], command: 'modal-g', context: 'modal' },
    ],
  });
  dispatcher.handleKey('g', 0); // 全局待定 short，期限 1000
  dispatcher.pushContext('modal'); // 重新裁决：modal-g 成为待定短命令
  assert.deepEqual(executed(), []);
  assert.equal(dispatcher.getState().pendingCommand, 'modal-g');
  assert.equal(dispatcher.getState().deadline, TIMEOUT); // 期限保持原值

  dispatcher.tick(TIMEOUT);
  assert.deepEqual(executed(), ['modal-g']); // 恰好到期只触发一次
  dispatcher.tick(TIMEOUT * 2);
  assert.deepEqual(executed(), ['modal-g']);
});

test('配置原子热更新：非法配置抛错且保留旧配置', () => {
  const { dispatcher, executed } = makeDispatcher(baseConfig);
  assert.throws(() =>
    dispatcher.setConfig({
      timeoutMs: TIMEOUT,
      bindings: [{ keys: [], command: 'bad' }],
    }),
  );
  assert.throws(() =>
    dispatcher.setConfig({ timeoutMs: 0, bindings: [] }),
  );
  // 旧配置仍可用
  dispatcher.handleKey('g', 0);
  dispatcher.handleKey('g', 1);
  assert.deepEqual(executed(), ['long']);
});

test('配置热更新：重新裁决待定序列，新配置生效且不重复触发', () => {
  const { dispatcher, executed } = makeDispatcher(baseConfig);
  dispatcher.handleKey('g', 0); // 旧配置下定待 short，期限 1000
  dispatcher.setConfig({
    timeoutMs: TIMEOUT,
    bindings: [{ keys: ['g'], command: 'reloaded-g' }],
  });
  assert.equal(dispatcher.getState().pendingCommand, 'reloaded-g');
  dispatcher.tick(TIMEOUT); // 期限沿用原值
  assert.deepEqual(executed(), ['reloaded-g']);
  dispatcher.tick(TIMEOUT * 2);
  assert.deepEqual(executed(), ['reloaded-g']);
});

test('配置热更新：新配置不再匹配已输入键时取消待定', () => {
  const { dispatcher, events, executed } = makeDispatcher(baseConfig);
  dispatcher.handleKey('g', 0);
  dispatcher.setConfig({
    timeoutMs: TIMEOUT,
    bindings: [{ keys: ['q'], command: 'q-cmd' }],
  });
  assert.equal(dispatcher.getState().typed.length, 0);
  assert.ok(events.some((e) => e.type === 'cancel' && e.reason === 'config'));
  dispatcher.tick(TIMEOUT * 2);
  assert.deepEqual(executed(), []);
});

test('暂停/恢复保存序列代际：暂停期间事件被忽略，恢复后续传', () => {
  const { dispatcher, events, executed } = makeDispatcher(baseConfig);
  dispatcher.handleKey('g', 0);
  const before = dispatcher.getState();
  assert.equal(before.generation, 1);
  assert.deepEqual(before.typed, ['g']);

  dispatcher.pause();
  dispatcher.handleKey('g', 100); // 暂停期间按键被忽略
  dispatcher.tick(TIMEOUT * 10); // 暂停期间时钟事件被忽略
  assert.deepEqual(executed(), []);

  dispatcher.resume();
  const after = dispatcher.getState();
  assert.equal(after.generation, 1); // 代际不变
  assert.deepEqual(after.typed, ['g']);
  assert.equal(after.deadline, TIMEOUT);

  dispatcher.tick(TIMEOUT); // 恢复后按原期限到期
  assert.deepEqual(executed(), ['short']);
  const exec = events.find((e) => e.type === 'execute');
  assert.ok(exec && exec.type === 'execute' && exec.generation === 1);
});

test('同刻混合事件重放：按键、焦点、配置切换按调用顺序确定性地应用', () => {
  const { dispatcher, executed } = makeDispatcher(baseConfig);
  const T = 5000;
  dispatcher.handleKey('g', T); // 待定 short，期限 T+1000
  dispatcher.setConfig({
    timeoutMs: TIMEOUT,
    bindings: [
      { keys: ['g'], command: 'reloaded-g' },
      { keys: ['g', 'g'], command: 'reloaded-gg' },
    ],
  }); // 同刻热更新：重新裁决为 reloaded-g
  dispatcher.handleKey('g', T); // 同刻第二键：完成 reloaded-gg
  assert.deepEqual(executed(), ['reloaded-gg']);

  dispatcher.handleKey('g', T); // 同刻新序列：待定 reloaded-g
  dispatcher.focusChanged(); // 同刻焦点变更：取消待定
  dispatcher.tick(T + TIMEOUT * 2);
  assert.deepEqual(executed(), ['reloaded-gg']);
});

test('时钟必须单调不减', () => {
  const { dispatcher } = makeDispatcher(baseConfig);
  dispatcher.tick(100);
  assert.throws(() => dispatcher.tick(50), /monotonic/);
});
