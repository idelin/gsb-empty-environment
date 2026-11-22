import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SchemaRegistry,
  continueProjection,
  projectErrorGraph,
  type Budgets,
  type ErrorNode,
  type ViewerContext,
} from '../src/index.ts';
import { KEY, PUB, TENANT, makeNode, raw, restricted, text, viewer } from './helpers.ts';
import { referenceProject } from './reference.ts';

const BIG: Budgets = { maxBytes: 1 << 20, maxNodes: 1 << 20 };
const msg = (s: string): Uint8Array => raw([1, text(s)]);

/**
 * 构造一个覆盖全部情形的小图：
 * 共享节点（两条权限路径）、隐藏分支先于公开分支、递归详情、未知类型。
 */
function buildGraph(): ErrorNode {
  const disclose = { message: PUB };
  const shared = makeNode({
    service: 'svc-shared',
    tenant: 't1',
    raw: msg('shared'),
    disclose,
  });
  const hidden = makeNode({
    service: 'svc-hidden',
    tenant: 't1',
    raw: msg('hidden'),
    visibility: restricted(5),
    disclose,
    causes: [shared],
  });
  const pub = makeNode({
    service: 'svc-pub',
    tenant: 't1',
    raw: msg('pub'),
    visibility: TENANT,
    disclose,
    causes: [shared],
  });
  const loop = makeNode({
    service: 'svc-loop',
    tenant: 't1',
    raw: msg('loop'),
    disclose,
  });
  loop.details = [loop, shared];
  const blob = makeNode({
    service: 'svc-blob',
    tenant: 't1',
    typeUrl: 'unknown/Blob',
    raw: text('opaque'),
    disclose,
  });
  return makeNode({
    service: 'gateway',
    tenant: 't1',
    raw: msg('root'),
    disclose,
    causes: [hidden, pub, blob],
    details: [loop],
  });
}

function registryV1(): SchemaRegistry {
  const registry = new SchemaRegistry();
  registry.register({
    typeUrl: 'svc/Error',
    fields: [{ index: 1, name: 'message', type: 'string' }],
  });
  return registry;
}

test('成对历史对照：引擎与独立解释器在每一步给出相同输出', () => {
  const root = buildGraph();
  const registry = registryV1();
  const alice = viewer('t1', 0, 1);
  const bob = viewer('t2', 0, 1);
  const admin = viewer('t1', 9, 1);

  const compare = (label: string, v: ViewerContext, budgets: Budgets): void => {
    const engine = projectErrorGraph(root, v, registry, budgets, KEY);
    const reference = referenceProject(root, v, registry, budgets);
    assert.equal(engine.text, reference.text, `${label}: text`);
    assert.equal(engine.truncated, reference.truncated, `${label}: truncated`);
    assert.equal(engine.nodesEmitted, reference.nodesEmitted, `${label}: nodes`);
  };

  // 历史步骤 1：注册表 v1，三种身份、多种预算。
  compare('v1/alice/full', alice, BIG);
  compare('v1/bob/full', bob, BIG);
  compare('v1/admin/full', admin, BIG);
  compare('v1/alice/tiny', alice, { maxBytes: 30, maxNodes: 100 });
  compare('v1/alice/one-node', alice, { maxBytes: 1 << 20, maxNodes: 1 });
  compare('v1/bob/empty-budget', bob, { maxBytes: 0, maxNodes: 0 });

  // 历史步骤 2：注册表热更新，同类型字段含义改变。
  registry.register({
    typeUrl: 'svc/Error',
    fields: [{ index: 1, name: 'summary', type: 'string' }],
  });
  // 节点只声明了 message 字段的披露，热更新后 summary 未声明 => fail-closed。
  compare('v2/alice/full', alice, BIG);
  compare('v2/admin/full', admin, BIG);

  // 历史步骤 3：游标续传拼接必须与解释器完整输出一致。
  const first = projectErrorGraph(root, alice, registry, { maxBytes: 60, maxNodes: 100 }, KEY);
  assert.ok(first.truncated);
  assert.ok(first.cursor !== null);
  const second = continueProjection(root, alice, registry, BIG, KEY, first.cursor);
  assert.equal(
    first.text + second.text,
    referenceProject(root, alice, registry, BIG).text,
    'v2/alice/resumed',
  );

  // 历史步骤 4：确定性 —— 同一历史重放两遍，逐字节相同。
  const replay1 = projectErrorGraph(root, admin, registry, BIG, KEY).text;
  const replay2 = projectErrorGraph(root, admin, registry, BIG, KEY).text;
  assert.equal(replay1, replay2);
});

test('成对历史对照：预算扫描下引擎与解释器始终一致', () => {
  const root = buildGraph();
  const registry = registryV1();
  const alice = viewer('t1', 0, 1);
  const full = projectErrorGraph(root, alice, registry, BIG, KEY).text;
  const total = Buffer.byteLength(full);

  // 对所有字节预算与节点预算的组合做对照。
  for (let budget = 0; budget <= total; budget += 7) {
    for (const maxNodes of [0, 1, 2, 100]) {
      const budgets = { maxBytes: budget, maxNodes };
      const engine = projectErrorGraph(root, alice, registry, budgets, KEY);
      const reference = referenceProject(root, alice, registry, budgets);
      assert.equal(engine.text, reference.text, `bytes=${budget} nodes=${maxNodes}`);
      assert.equal(engine.truncated, reference.truncated);
      // 输出永远是完整文本的行前缀（字段边界）。
      assert.ok(full.startsWith(engine.text));
      assert.ok(engine.text === '' || engine.text.endsWith('\n'));
    }
  }
});
