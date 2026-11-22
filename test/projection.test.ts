import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import {
  SchemaRegistry,
  disclosurePermits,
  projectErrorGraph,
  publicCertificate,
  recordDiagnostic,
  type ErrorNode,
  type ViewerContext,
} from '../src/index.ts';
import { KEY, PUB, TENANT, frames, makeNode, raw, restricted, text, viewer } from './helpers.ts';
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
const discloseMessage = { message: PUB };

test('共享节点经两种权限路径：只出现一次，序号确定', () => {
  const registry = errorRegistry();
  const shared = makeNode({
    service: 'svc-shared',
    tenant: 't1',
    raw: msg('shared'),
    disclose: discloseMessage,
  });
  const hidden = makeNode({
    service: 'svc-hidden',
    tenant: 't1',
    raw: msg('hidden-msg'),
    visibility: restricted(5),
    disclose: discloseMessage,
    causes: [shared],
  });
  const pub = makeNode({
    service: 'svc-pub',
    tenant: 't1',
    raw: msg('pub'),
    visibility: TENANT,
    disclose: discloseMessage,
    causes: [shared],
  });
  const root = makeNode({
    service: 'gateway',
    tenant: 't1',
    raw: msg('root'),
    disclose: discloseMessage,
    causes: [hidden, pub],
  });

  const result = projectErrorGraph(root, viewer('t1'), registry, BIG, KEY);
  const expected = frames(
    '["H",1]',
    '["N",0,"gateway","t1","svc/Error",0]',
    '["F",0,"message","root"]',
    '["C",0,1]',
    '["N",1,"svc-pub","t1","svc/Error",0]',
    '["F",1,"message","pub"]',
    '["C",1,2]',
    '["N",2,"svc-shared","t1","svc/Error",0]',
    '["F",2,"message","shared"]',
  );
  assert.equal(result.text, expected);
  assert.equal(result.truncated, false);
  assert.equal(result.nodesEmitted, 3);
  assert.equal(result.bytesEmitted, expected.length);
  assert.equal(result.cursor, null);
  // 共享节点只序列化一次。
  assert.equal(result.text.split('["N"').length - 1, 3);
  // 与独立解释器逐字节一致。
  assert.equal(referenceProject(root, viewer('t1'), registry, BIG).text, expected);
});

test('隐藏分支先于公开分支：公开序号不留空洞', () => {
  const registry = errorRegistry();
  const mk = (service: string, message: string, hiddenNode: boolean): ErrorNode =>
    makeNode({
      service,
      tenant: 't1',
      raw: msg(message),
      visibility: hiddenNode ? restricted(9) : PUB,
      disclose: discloseMessage,
    });
  const a = mk('svc-a', 'a', false);
  const b = mk('svc-b', 'b', false);
  const root = makeNode({
    service: 'gateway',
    tenant: 't1',
    raw: msg('root'),
    disclose: discloseMessage,
    causes: [mk('svc-h1', 'h1', true), mk('svc-h2', 'h2', true), a, b],
  });

  const result = projectErrorGraph(root, viewer('t1'), registry, BIG, KEY);
  assert.equal(
    result.text,
    frames(
      '["H",1]',
      '["N",0,"gateway","t1","svc/Error",0]',
      '["F",0,"message","root"]',
      '["C",0,1]',
      '["C",0,2]',
      '["N",1,"svc-a","t1","svc/Error",0]',
      '["F",1,"message","a"]',
      '["N",2,"svc-b","t1","svc/Error",0]',
      '["F",2,"message","b"]',
    ),
  );
  // 原因数量只统计可见边。
  assert.equal(result.text.split('["C"').length - 1, 2);
});

test('递归详情：环被去重为序号引用，输出确定且终止', () => {
  const registry = errorRegistry();
  const a = makeNode({
    service: 'svc-a',
    tenant: 't1',
    raw: msg('a'),
    disclose: discloseMessage,
  });
  const b = makeNode({
    service: 'svc-b',
    tenant: 't1',
    raw: msg('b'),
    disclose: discloseMessage,
  });
  a.details = [b];
  b.details = [a];

  const result = projectErrorGraph(a, viewer('t1'), registry, BIG, KEY);
  assert.equal(
    result.text,
    frames(
      '["H",1]',
      '["N",0,"svc-a","t1","svc/Error",0]',
      '["F",0,"message","a"]',
      '["D",0,1]',
      '["N",1,"svc-b","t1","svc/Error",0]',
      '["F",1,"message","b"]',
      '["D",1,0]',
    ),
  );
});

test('逐字段披露：未声明字段 fail-closed，restricted 按级别放行', () => {
  const registry = new SchemaRegistry();
  registry.register({
    typeUrl: 'svc/Error',
    fields: [
      { index: 1, name: 'message', type: 'string' },
      { index: 2, name: 'trace', type: 'string' },
      { index: 3, name: 'internal', type: 'string' },
      { index: 4, name: 'extra', type: 'string' },
    ],
  });
  const root = makeNode({
    service: 'svc',
    tenant: 't1',
    raw: raw(
      [1, text('m')],
      [2, text('t')],
      [3, text('i')],
      [4, text('e')],
    ),
    disclose: { message: PUB, trace: TENANT, internal: restricted(3) },
  });

  const low = projectErrorGraph(root, viewer('t1', 0), registry, BIG, KEY);
  assert.equal(
    low.text,
    frames(
      '["H",1]',
      '["N",0,"svc","t1","svc/Error",0]',
      '["F",0,"message","m"]',
      '["F",0,"trace","t"]',
    ),
  );

  const high = projectErrorGraph(root, viewer('t1', 3), registry, BIG, KEY);
  assert.equal(
    high.text,
    frames(
      '["H",1]',
      '["N",0,"svc","t1","svc/Error",0]',
      '["F",0,"message","m"]',
      '["F",0,"trace","t"]',
      '["F",0,"internal","i"]',
    ),
  );

  // 其他租户看不到 tenant 级字段。
  const outsider = projectErrorGraph(root, viewer('t9', 9), registry, BIG, KEY);
  assert.equal(
    outsider.text,
    frames(
      '["H",1]',
      '["N",0,"svc","t1","svc/Error",0]',
      '["F",0,"message","m"]',
      '["F",0,"internal","i"]',
    ),
  );
});

test('未知类型与畸形字节都被隔离：不输出字段，不泄漏原始字节', () => {
  const registry = errorRegistry();
  const unknown = makeNode({
    service: 'svc-x',
    tenant: 't1',
    typeUrl: 'unknown/Blob',
    raw: text('secret-bytes'),
    disclose: { anything: PUB },
  });
  const malformed = makeNode({
    service: 'svc-y',
    tenant: 't1',
    raw: Uint8Array.of(0x80), // 截断的 varint
    disclose: { message: PUB },
  });
  const root = makeNode({
    service: 'gateway',
    tenant: 't1',
    raw: msg('root'),
    disclose: discloseMessage,
    causes: [unknown, malformed],
  });

  const result = projectErrorGraph(root, viewer('t1'), registry, BIG, KEY);
  assert.equal(
    result.text,
    frames(
      '["H",1]',
      '["N",0,"gateway","t1","svc/Error",0]',
      '["F",0,"message","root"]',
      '["C",0,1]',
      '["C",0,2]',
      '["N",1,"svc-x","t1","unknown/Blob",1]',
      '["N",2,"svc-y","t1","svc/Error",1]',
    ),
  );
  assert.ok(!result.text.includes('secret-bytes'));
});

/** 把图中对 viewer 不可见的节点整体剪掉，保持共享与环。 */
function pruneInvisible(root: ErrorNode, v: ViewerContext): ErrorNode | null {
  const memo = new Map<ErrorNode, ErrorNode>();
  const go = (n: ErrorNode): ErrorNode | null => {
    if (!disclosurePermits(n.visibility, v, n.tenant)) return null;
    const hit = memo.get(n);
    if (hit !== undefined) return hit;
    const copy: ErrorNode = {
      service: n.service,
      tenant: n.tenant,
      typeUrl: n.typeUrl,
      raw: n.raw,
      visibility: n.visibility,
      disclose: n.disclose,
      causes: [],
      details: [],
    };
    memo.set(n, copy);
    for (const c of n.causes) {
      const p = go(c);
      if (p !== null) copy.causes.push(p);
    }
    for (const d of n.details) {
      const p = go(d);
      if (p !== null) copy.details.push(p);
    }
    return copy;
  };
  return go(root);
}

test('不泄漏隐藏节点的存在：投影与剪掉隐藏节点后的投影逐字节相同', () => {
  const registry = errorRegistry();
  const shared = makeNode({
    service: 'svc-shared',
    tenant: 't1',
    raw: msg('shared'),
    disclose: discloseMessage,
  });
  const hiddenSub = makeNode({
    service: 'svc-hidden',
    tenant: 't2',
    raw: msg('hidden'),
    visibility: TENANT, // viewer 属于 t1，看不到 t2 节点
    disclose: discloseMessage,
    causes: [shared],
  });
  const loop = makeNode({
    service: 'svc-loop',
    tenant: 't1',
    raw: msg('loop'),
    disclose: discloseMessage,
  });
  loop.details = [loop, hiddenSub];
  const root = makeNode({
    service: 'gateway',
    tenant: 't1',
    raw: msg('root'),
    disclose: discloseMessage,
    causes: [hiddenSub, shared],
    details: [loop],
  });

  const v = viewer('t1');
  const full = projectErrorGraph(root, v, registry, BIG, KEY);
  const pruned = pruneInvisible(root, v);
  assert.ok(pruned !== null);
  const projected = projectErrorGraph(pruned, v, registry, BIG, KEY);

  assert.equal(full.text, projected.text);
  assert.equal(full.bytesEmitted, projected.bytesEmitted);
  assert.equal(full.nodesEmitted, projected.nodesEmitted);
  assert.equal(full.truncated, projected.truncated);
  // 隐藏节点既不占序号也不占字节。
  assert.ok(!full.text.includes('svc-hidden'));
  assert.ok(!full.text.includes('hidden'));
});

test('后台可保存完整诊断，公开证书仅含获准字节', () => {
  const registry = errorRegistry();
  const secret = makeNode({
    service: 'svc-internal',
    tenant: 't1',
    raw: msg('internal-secret'),
    visibility: restricted(7),
    disclose: { message: PUB },
  });
  const root = makeNode({
    service: 'gateway',
    tenant: 't1',
    raw: msg('root'),
    disclose: discloseMessage,
    causes: [secret],
  });

  const diagnostic = recordDiagnostic(root, registry);
  assert.ok(diagnostic.fullText.includes('internal-secret'));
  assert.ok(diagnostic.fullText.includes('svc-internal'));

  const result = projectErrorGraph(root, viewer('t1'), registry, BIG, KEY);
  const cert = publicCertificate(result, registry);
  const certText = Buffer.from(cert.bytes).toString('utf8');
  assert.ok(!certText.includes('internal-secret'));
  assert.ok(!certText.includes('svc-internal'));
  assert.equal(cert.byteLength, result.bytes.length);
  assert.equal(cert.sha256, createHash('sha256').update(result.bytes).digest('hex'));
  assert.equal(cert.registryVersion, registry.version);
});
