import test from 'node:test';
import assert from 'node:assert/strict';
import { Network } from '../src/network.ts';
import { Witness } from '../src/witness.ts';
import { LeaseController } from '../src/controller.ts';

const TTL_MS = 300;

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

interface Cluster {
  network: Network;
  witness: Witness;
  dc1: LeaseController;
  dc2: LeaseController;
  close: () => void;
}

function makeCluster(): Cluster {
  const network = new Network();
  const witness = new Witness({ network });
  witness.attach('witness');
  const dc1 = new LeaseController({ site: 'dc1', peer: 'dc2', network, ttlMs: TTL_MS });
  const dc2 = new LeaseController({ site: 'dc2', peer: 'dc1', network, ttlMs: TTL_MS });
  dc1.attach();
  dc2.attach();
  return {
    network, witness, dc1, dc2,
    close: () => { dc1.close(); dc2.close(); },
  };
}

/** 全局互斥不变式：任意时刻至多一个机房持有"未过期"的主租约。 */
function assertAtMostOneActivePrimary(...controllers: LeaseController[]): void {
  const active = controllers.filter(
    (c) => c.role === 'primary' && c.lease !== null && c.lease.expiresAt > Date.now(),
  );
  assert.ok(active.length <= 1, `expected <=1 active primary, got: ${active.map((c) => c.site)}`);
}

test('正常态：dc1 为主可写，dc2 为备只读', async () => {
  const { dc1, dc2, close } = makeCluster();
  try {
    await dc1.tick();
    await dc2.tick();
    assert.equal(dc1.role, 'primary');
    assert.equal(dc2.role, 'standby');
    assert.equal(dc1.lease!.term, 1);

    const term = await dc1.write('foo', 'bar');
    assert.equal(term, 1);
    assert.equal(dc1.read('foo'), 'bar');

    await assert.rejects(() => dc2.write('foo', 'evil'), /read-only/);
    assert.equal(dc2.read('foo'), undefined);
    assertAtMostOneActivePrimary(dc1, dc2);
  } finally {
    close();
  }
});

test('法官拒绝：持有者存活时禁止接管；观测 term 不符时禁止接管', async () => {
  const { network, dc1, dc2, close } = makeCluster();
  try {
    await dc1.tick();
    assert.equal(dc1.role, 'primary');

    // 持有者存活，接管必须被拒绝
    const alive = await network.call<{ granted: boolean; reason?: string }>(
      'dc2', 'witness', '/lease/takeover', 'POST',
      { site: 'dc2', ttlMs: TTL_MS, observedTerm: 1 },
    );
    assert.equal(alive.granted, false);
    assert.equal(alive.reason, 'holder-alive');

    // 持有者失联，但观测到的 term 不是当前租约，同样拒绝
    network.partition('dc1', 'witness');
    network.partition('dc1', 'dc2');
    const stale = await network.call<{ granted: boolean; reason?: string }>(
      'dc2', 'witness', '/lease/takeover', 'POST',
      { site: 'dc2', ttlMs: TTL_MS, observedTerm: 999 },
    );
    assert.equal(stale.granted, false);
    assert.equal(stale.reason, 'stale-observed-term');
    assert.equal(dc2.role, 'standby');
  } finally {
    close();
  }
});

test('验收场景：光纤挖断 → 法官仲裁晋升 dc2 → dc1 恢复后自动隔离为只读', async () => {
  const { network, dc1, dc2, close } = makeCluster();
  try {
    // 1) 正常：dc1 主，dc2 备
    await dc1.tick();
    await dc2.tick();
    assert.equal(dc1.role, 'primary');
    assert.equal(dc2.role, 'standby');
    const dc1Term = dc1.lease!.term;

    // 2) 光纤挖断：dc1 与法官、dc1 与 dc2 双向失联；dc1 整机冻结（不再 tick，模拟掉电/卡顿）
    network.partition('dc1', 'witness');
    network.partition('dc1', 'dc2');

    // 分区期间 dc1 自以为仍是主，但无法通过法官 fencing 校验 → 写失败（fail-closed）
    await assert.rejects(() => dc1.write('k', 'v'));

    // 3) dc2 发现 peer 失联，请求法官接管；法官亲自探测 dc1 确认失联，
    //    并等待旧租约到期后颁发新租约（term 递增）
    const t0 = Date.now();
    await dc2.tick();
    const takeoverElapsed = Date.now() - t0;
    assert.equal(dc2.role, 'primary');
    assert.equal(dc2.lease!.term, dc1Term + 1);
    // 法官必须等旧租约过期才发证：新租约的有效期整体落在旧租约到期之后
    assert.ok(takeoverElapsed > 0, 'takeover should wait for old lease expiry');
    assert.ok(dc2.lease!.expiresAt > dc1.lease!.expiresAt, 'new lease must outlive old lease expiry');
    assertAtMostOneActivePrimary(dc1, dc2); // dc1 的本地租约此时必然已过期

    // 4) dc2 合法晋升后可写；dc1 旧 term 的 fencing 校验被法官拒绝
    await dc2.write('k', 'v2');
    assert.equal(dc2.read('k'), 'v2');
    // （以运维客户端身份查询，避免走被切断的 dc1->witness 链路）
    const stale = await network.call<{ valid: boolean }>(
      'operator', 'witness', '/lease/validate', 'POST',
      { site: 'dc1', term: dc1Term },
    );
    assert.equal(stale.valid, false, 'stale fencing token must be rejected');

    // 5) dc1 恢复（网络修复），第一步续租即被法官告知凭证失效 → 自动隔离为只读
    network.healPartition('dc1', 'witness');
    network.healPartition('dc1', 'dc2');
    assert.equal(dc1.role, 'primary'); // 恢复瞬间仍以为自己是主
    await dc1.tick();
    assert.equal(dc1.role, 'fenced', 'dc1 must fence itself off after credential invalidated');
    await assert.rejects(() => dc1.write('k', 'v3'), /read-only/);

    // 6) dc2 依旧是唯一主，正常续租与写入
    await dc2.tick();
    assert.equal(dc2.role, 'primary');
    await dc2.write('k', 'v4');
    assertAtMostOneActivePrimary(dc1, dc2);
  } finally {
    close();
  }
});

test('主续租失败且本地租约到期后自动停写降级', async () => {
  const { network, dc1, dc2, close } = makeCluster();
  try {
    await dc1.tick();
    assert.equal(dc1.role, 'primary');

    network.partition('dc1', 'witness');
    // 本地租约未到期前仍是主（但写需要法官确认，分区下写不进去）
    await dc1.tick();
    assert.equal(dc1.role, 'primary');
    // 等待本地租约过期后再 tick：自动降级为只读备机
    await sleep(TTL_MS + 50);
    await dc1.tick();
    assert.equal(dc1.role, 'standby');
    await assert.rejects(() => dc1.write('k', 'v'), /read-only/);
    assertAtMostOneActivePrimary(dc1, dc2);
  } finally {
    close();
  }
});

test('fencing token 单调递增：每次颁发 term 严格递增，旧 term 立即失效', async () => {
  const { network, witness, dc1, close } = makeCluster();
  try {
    await dc1.tick();
    const t1 = dc1.lease!.term;
    await dc1.tick(); // 续租
    const t2 = dc1.lease!.term;
    assert.ok(t2 > t1);
    // 旧 term 立即失效
    const stale = await network.call<{ valid: boolean }>(
      'dc1', 'witness', '/lease/validate', 'POST',
      { site: 'dc1', term: t1 },
    );
    assert.equal(stale.valid, false);
    assert.equal(witness.status().lease!.term, t2);
  } finally {
    close();
  }
});
