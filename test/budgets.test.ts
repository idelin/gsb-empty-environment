import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CursorError,
  SchemaRegistry,
  continueProjection,
  projectErrorGraph,
  type ErrorNode,
} from '../src/index.ts';
import { KEY, PUB, frames, makeNode, raw, text, viewer } from './helpers.ts';
import { referenceProject } from './reference.ts';

const BIG = { maxBytes: 1 << 20, maxNodes: 1 << 20 };

function errorRegistry(): SchemaRegistry {
  const registry = new SchemaRegistry();
  registry.register({
    typeUrl: 'svc/Error',
    fields: [{ index: 1, name: 'message', type: 'string' }],
  });
  return registry;
}

const msg = (s: string): Uint8Array => raw([1, text(s)]);

/** gateway -> svc-a -> svc-b 的短链。 */
function chain(): { root: ErrorNode; registry: SchemaRegistry } {
  const registry = errorRegistry();
  const b = makeNode({
    service: 'svc-b',
    tenant: 't1',
    raw: msg('b'),
    disclose: { message: PUB },
  });
  const a = makeNode({
    service: 'svc-a',
    tenant: 't1',
    raw: msg('a'),
    disclose: { message: PUB },
    causes: [b],
  });
  const root = makeNode({
    service: 'gateway',
    tenant: 't1',
    raw: msg('root'),
    disclose: { message: PUB },
    causes: [a],
  });
  return { root, registry };
}

test('恰好只能放下空字符串的预算：输出为空串且确定', () => {
  const { root, registry } = chain();
  const result = projectErrorGraph(
    root,
    viewer('t1'),
    registry,
    { maxBytes: 0, maxNodes: 100 },
    KEY,
  );
  assert.equal(result.text, '');
  assert.equal(result.bytesEmitted, 0);
  assert.equal(result.nodesEmitted, 0);
  assert.equal(result.truncated, true);
  assert.ok(result.cursor !== null);

  // 同一游标可继续，并还原完整输出。
  const rest = continueProjection(root, viewer('t1'), registry, BIG, KEY, result.cursor);
  const full = projectErrorGraph(root, viewer('t1'), registry, BIG, KEY);
  assert.equal(rest.text, full.text);
  assert.equal(rest.truncated, false);
});

test('截断只发生在字段边界，游标续传拼接等于完整输出', () => {
  const { root, registry } = chain();
  const v = viewer('t1');
  const full = projectErrorGraph(root, v, registry, BIG, KEY).text;
  const lines = full.split('\n').filter((line) => line.length > 0);

  // 预算 = 前 4 帧（含一条 C 边）的字节数。
  const head = frames(...lines.slice(0, 4));
  const first = projectErrorGraph(
    root,
    v,
    registry,
    { maxBytes: Buffer.byteLength(head), maxNodes: 100 },
    KEY,
  );
  assert.equal(first.text, head);
  assert.equal(first.truncated, true);
  assert.ok(first.cursor !== null);
  // 每一帧都完整：输出是完整文本的行前缀。
  assert.ok(full.startsWith(first.text));
  assert.ok(first.text.endsWith('\n'));

  const second = continueProjection(root, v, registry, BIG, KEY, first.cursor);
  assert.equal(second.truncated, false);
  assert.equal(second.cursor, null);
  assert.equal(first.text + second.text, full);
});

test('预算落在帧中间时仍停在字段边界', () => {
  const { root, registry } = chain();
  const v = viewer('t1');
  const full = projectErrorGraph(root, v, registry, BIG, KEY).text;
  // 选一个必然落在某帧中间的预算。
  const budget = Buffer.byteLength(full) - 3;
  const result = projectErrorGraph(root, v, registry, { maxBytes: budget, maxNodes: 100 }, KEY);
  assert.equal(result.truncated, true);
  assert.ok(full.startsWith(result.text));
  assert.ok(result.text === '' || result.text.endsWith('\n'));
  for (const line of result.text.split('\n').filter((l) => l.length > 0)) {
    assert.doesNotThrow(() => JSON.parse(line));
  }
});

test('节点预算：只放出前 N 个节点，剩余经游标续传', () => {
  const { root, registry } = chain();
  const v = viewer('t1');
  const full = projectErrorGraph(root, v, registry, BIG, KEY).text;

  const first = projectErrorGraph(root, v, registry, { maxBytes: 1 << 20, maxNodes: 1 }, KEY);
  assert.equal(first.nodesEmitted, 1);
  assert.equal(first.truncated, true);
  assert.ok(first.cursor !== null);

  const second = continueProjection(root, v, registry, BIG, KEY, first.cursor);
  assert.equal(first.text + second.text, full);
});

test('游标不能跨租户、注册表或权限代次复用，也不能被篡改', () => {
  const { root, registry } = chain();
  const v = viewer('t1', 0, 1);
  const first = projectErrorGraph(root, v, registry, { maxBytes: 10, maxNodes: 100 }, KEY);
  assert.ok(first.cursor !== null);
  const cursor = first.cursor;

  // 跨租户
  assert.throws(
    () => continueProjection(root, viewer('t2', 0, 1), registry, BIG, KEY, cursor),
    CursorError,
  );
  // 跨权限代次
  assert.throws(
    () => continueProjection(root, viewer('t1', 0, 2), registry, BIG, KEY, cursor),
    CursorError,
  );
  // 跨注册表版本（热更新后）
  registry.register({
    typeUrl: 'svc/Error',
    fields: [{ index: 1, name: 'message', type: 'string' }],
  });
  assert.throws(
    () => continueProjection(root, v, registry, BIG, KEY, cursor),
    CursorError,
  );
  // 错误的密钥
  const fresh = projectErrorGraph(root, v, errorRegistry(), { maxBytes: 10, maxNodes: 100 }, KEY);
  assert.ok(fresh.cursor !== null);
  assert.throws(
    () => continueProjection(root, v, errorRegistry(), BIG, 'wrong-key', fresh.cursor as string),
    CursorError,
  );
  // 篡改负载
  const tampered = `${fresh.cursor?.slice(0, -2)}xx`;
  assert.throws(
    () => continueProjection(root, v, errorRegistry(), BIG, KEY, tampered),
    CursorError,
  );
});

test('游标绑定具体投影计划：图被改动后不能复用', () => {
  const { root, registry } = chain();
  const v = viewer('t1');
  const first = projectErrorGraph(root, v, registry, { maxBytes: 10, maxNodes: 100 }, KEY);
  assert.ok(first.cursor !== null);

  // 改动图：追加一个可见原因，计划摘要随之改变。
  const extra = makeNode({
    service: 'svc-extra',
    tenant: 't1',
    raw: msg('x'),
    disclose: { message: PUB },
  });
  root.causes.push(extra);
  assert.throws(
    () => continueProjection(root, v, registry, BIG, KEY, first.cursor as string),
    CursorError,
  );
});

test('注册表热更新：同类型同字节按新字段含义解码', () => {
  const registry = new SchemaRegistry();
  registry.register({
    typeUrl: 'svc/Error',
    fields: [{ index: 1, name: 'label', type: 'string' }],
  });
  // 单字节 0x2a：按 string 解码为 "*"，按 varint 解码为 42。
  const root = makeNode({
    service: 'svc',
    tenant: 't1',
    raw: raw([1, Uint8Array.of(0x2a)]),
    disclose: { label: PUB, code: PUB },
  });
  const v = viewer('t1');

  const before = projectErrorGraph(root, v, registry, BIG, KEY);
  assert.equal(
    before.text,
    frames('["H",1]', '["N",0,"svc","t1","svc/Error",0]', '["F",0,"label","*"]'),
  );

  registry.register({
    typeUrl: 'svc/Error',
    fields: [{ index: 1, name: 'code', type: 'varint' }],
  });
  const after = projectErrorGraph(root, v, registry, BIG, KEY);
  assert.equal(
    after.text,
    frames('["H",2]', '["N",0,"svc","t1","svc/Error",0]', '["F",0,"code",42]'),
  );

  // 独立解释器对两个注册表版本给出相同结果。
  assert.equal(referenceProject(root, v, registry, BIG).text, after.text);
});
