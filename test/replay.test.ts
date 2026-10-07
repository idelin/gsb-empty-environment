import assert from 'node:assert/strict';
import test from 'node:test';
import { ReplayEngine, type Operation, type Receipt, type Transport } from '../src/replay.ts';
import { MemoryStorage } from '../src/storage.ts';
import { collectRefs, rewriteRefs } from '../src/rewrite.ts';

/** 可控传输层：按 opId 脚本化返回/抛错，并记录实际发送内容。 */
function scriptedTransport(
  script: Record<string, (op: Operation) => Receipt | Promise<Receipt>>,
): { transport: Transport; sent: Operation[] } {
  const sent: Operation[] = [];
  const transport: Transport = async (op) => {
    sent.push(structuredClone(op));
    const handler = script[op.id];
    if (!handler) throw new Error(`unexpected op sent: ${op.id}`);
    return handler(op);
  };
  return { transport, sent };
}

const ok = (serverIds?: Record<string, string>) =>
  (_op: Operation): Receipt => ({ opId: _op.id, ok: true, serverIds });
const fail = (error: string) =>
  (op: Operation): Receipt => ({ opId: op.id, ok: false, error });

test('rewriteRefs 递归替换嵌套引用，collectRefs 收集字符串叶子', () => {
  const tree = {
    blocks: [{ parent: 'tmp:1', text: 'hi' }, { meta: { ref: 'tmp:1', n: 3 } }],
    flag: true,
    nothing: null,
  };
  assert.deepEqual(rewriteRefs(tree, { 'tmp:1': 'srv-1' }), {
    blocks: [{ parent: 'srv-1', text: 'hi' }, { meta: { ref: 'srv-1', n: 3 } }],
    flag: true,
    nothing: null,
  });
  assert.deepEqual([...collectRefs(tree)].sort(), ['hi', 'tmp:1']);
});

test('拓扑序：消费者先入队，生产者仍先发送', async () => {
  const { transport, sent } = scriptedTransport({
    'op-create': ok({ 'tmp:doc': 'srv-doc-1' }),
    'op-insert': ok(),
  });
  const engine = new ReplayEngine(transport);
  engine.enqueue({ id: 'op-insert', kind: 'insert', payload: { parent: 'tmp:doc' } });
  engine.enqueue({ id: 'op-create', kind: 'create', payload: {}, creates: 'tmp:doc' });

  const report = await engine.flush();
  assert.deepEqual(sent.map((op) => op.id), ['op-create', 'op-insert']);
  assert.deepEqual(report.done.sort(), ['op-create', 'op-insert']);
  // 发送前引用已被改写为服务器 id
  assert.deepEqual(sent[1].payload, { parent: 'srv-doc-1' });
});

test('部分失败：创建失败仅阻断依赖者，独立操作继续', async () => {
  const { transport, sent } = scriptedTransport({
    'op-create': fail('validation: title required'),
    'op-independent': ok(),
  });
  const engine = new ReplayEngine(transport);
  engine.enqueue({ id: 'op-create', kind: 'create', payload: {}, creates: 'tmp:doc' });
  engine.enqueue({ id: 'op-insert', kind: 'insert', payload: { parent: 'tmp:doc' } });
  engine.enqueue({ id: 'op-independent', kind: 'update', payload: { v: 1 } });

  const report = await engine.flush();
  assert.equal(engine.status('op-create'), 'failed');
  assert.equal(engine.status('op-insert'), 'blocked');
  assert.equal(engine.status('op-independent'), 'done');
  // 被阻断的操作从未发送
  assert.deepEqual(sent.map((op) => op.id).sort(), ['op-create', 'op-independent']);
  assert.deepEqual(report.blocked, ['op-insert']);
});

test('传递阻断：失败沿依赖链传播到间接后继', async () => {
  const { transport, sent } = scriptedTransport({
    a: fail('boom'),
    d: ok(),
  });
  const engine = new ReplayEngine(transport);
  engine.enqueue({ id: 'a', kind: 'create', payload: {}, creates: 'tmp:a' });
  engine.enqueue({ id: 'b', kind: 'insert', payload: { parent: 'tmp:a' }, creates: 'tmp:b' });
  engine.enqueue({ id: 'c', kind: 'insert', payload: { parent: 'tmp:b' } });
  engine.enqueue({ id: 'd', kind: 'update', payload: {} });

  await engine.flush();
  assert.equal(engine.status('a'), 'failed');
  assert.equal(engine.status('b'), 'blocked');
  assert.equal(engine.status('c'), 'blocked');
  assert.equal(engine.status('d'), 'done');
  assert.deepEqual(sent.map((op) => op.id).sort(), ['a', 'd']);
});

test('链式临时 id：id 映射逐跳递归改写给未发送操作', async () => {
  const { transport, sent } = scriptedTransport({
    'op-doc': ok({ 'tmp:doc': 'srv-doc' }),
    'op-block': ok({ 'tmp:block': 'srv-block' }),
    'op-text': ok(),
  });
  const engine = new ReplayEngine(transport);
  engine.enqueue({ id: 'op-doc', kind: 'create-doc', payload: {}, creates: 'tmp:doc' });
  engine.enqueue({
    id: 'op-block',
    kind: 'create-block',
    payload: { doc: 'tmp:doc', attrs: { anchor: ['tmp:doc'] } },
    creates: 'tmp:block',
  });
  engine.enqueue({
    id: 'op-text',
    kind: 'insert-text',
    payload: { into: { block: 'tmp:block' }, also: 'tmp:doc' },
  });

  await engine.flush();
  assert.deepEqual(sent.map((op) => op.id), ['op-doc', 'op-block', 'op-text']);
  // op-block 发送时 tmp:doc 已改写，tmp:block 仍是临时 id（由它自己创建）
  assert.deepEqual(sent[1].payload, { doc: 'srv-doc', attrs: { anchor: ['srv-doc'] } });
  // op-text 发送时两级映射都已生效
  assert.deepEqual(sent[2].payload, { into: { block: 'srv-block' }, also: 'srv-doc' });
  assert.deepEqual(engine.idMappings(), { 'tmp:doc': 'srv-doc', 'tmp:block': 'srv-block' });
});

test('重复回执幂等：二次应用无副作用，已完成操作不重发', async () => {
  const { transport, sent } = scriptedTransport({ a: ok({ 'tmp:a': 'srv-a' }) });
  const engine = new ReplayEngine(transport);
  engine.enqueue({ id: 'a', kind: 'create', payload: {}, creates: 'tmp:a' });

  await engine.flush();
  assert.equal(sent.length, 1);

  const receipt: Receipt = { opId: 'a', ok: true, serverIds: { 'tmp:a': 'srv-a' } };
  assert.equal(engine.applyReceipt(receipt), false); // 重复回执被丢弃
  assert.equal(engine.applyReceipt({ opId: 'ghost', ok: true }), false); // 未知操作
  assert.deepEqual(engine.idMappings(), { 'tmp:a': 'srv-a' });

  const report = await engine.flush(); // 无可执行操作
  assert.equal(sent.length, 1);
  assert.deepEqual(report.sent, []);
});

test('重启恢复：快照恢复进度与 id 映射，服务器重投回执安全', async () => {
  const storage = new MemoryStorage();

  // 第一次运行：op-1 完成并持久化，op-2 尚未入队
  const first = scriptedTransport({ 'op-1': ok({ 'tmp:1': 'srv-1' }) });
  const engine1 = new ReplayEngine(first.transport, storage);
  engine1.enqueue({ id: 'op-1', kind: 'create', payload: {}, creates: 'tmp:1' });
  await engine1.flush();
  assert.equal(engine1.status('op-1'), 'done');

  // 崩溃重启：同一 storage，新 transport；重启后再入队引用旧临时 id 的操作
  const second = scriptedTransport({ 'op-2': ok() });
  const engine2 = new ReplayEngine(second.transport, storage);
  assert.equal(engine2.status('op-1'), 'done');
  assert.deepEqual(engine2.idMappings(), { 'tmp:1': 'srv-1' });
  engine2.enqueue({ id: 'op-2', kind: 'insert', payload: { parent: 'tmp:1' } });

  // 服务器重投 op-1 的回执（重启前已处理过）：幂等丢弃
  assert.equal(engine2.applyReceipt({ opId: 'op-1', ok: true, serverIds: { 'tmp:1': 'srv-1' } }), false);

  const report = await engine2.flush();
  assert.deepEqual(report.sent, ['op-2']); // op-1 不重发
  assert.deepEqual(second.sent[0].payload, { parent: 'srv-1' }); // 入队即改写
  assert.equal(engine2.status('op-2'), 'done');
});

test('失败后重试：requeue 解除阻断并重新执行', async () => {
  const storage = new MemoryStorage();
  let createAttempts = 0;
  const transport: Transport = async (op) => {
    if (op.id === 'op-create') {
      createAttempts += 1;
      if (createAttempts === 1) return { opId: op.id, ok: false, error: 'transient' };
      return { opId: op.id, ok: true, serverIds: { 'tmp:doc': 'srv-doc' } };
    }
    return { opId: op.id, ok: true };
  };
  const engine = new ReplayEngine(transport, storage);
  engine.enqueue({ id: 'op-create', kind: 'create', payload: {}, creates: 'tmp:doc' });
  engine.enqueue({ id: 'op-insert', kind: 'insert', payload: { parent: 'tmp:doc' } });

  await engine.flush();
  assert.equal(engine.status('op-create'), 'failed');
  assert.equal(engine.status('op-insert'), 'blocked');

  engine.requeueAll();
  const report = await engine.flush();
  assert.deepEqual(report.done.sort(), ['op-create', 'op-insert']);
  assert.equal(createAttempts, 2);
});
