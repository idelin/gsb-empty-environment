import { test } from 'node:test';
import assert from 'node:assert/strict';
import { VirtualClock } from '../src/clock.ts';
import { Cluster } from '../src/store.ts';
import { Session } from '../src/session.ts';
import { SessionRouter } from '../src/router.ts';
import { NoFreshReplicaError, ReadTimeoutError } from '../src/errors.ts';

function setup() {
  const clock = new VirtualClock();
  const cluster = new Cluster(clock);
  const router = new SessionRouter(cluster, clock);
  return { clock, cluster, router };
}

/** 发起写入并推进时钟使主库生效（复制可能仍在途中）。 */
async function writeAndSettle(
  router: SessionRouter,
  clock: VirtualClock,
  session: Session,
  partition: string,
  key: string,
  value: unknown,
  primaryLatencyMs: number,
): Promise<number> {
  const pending = router.write(session, partition, key, value);
  await clock.advance(primaryLatencyMs);
  return pending;
}

test('wait 策略：副本落后时挂起等待，追平后返回新版本', async () => {
  const { clock, cluster, router } = setup();
  cluster.addPartition('p1', {
    primaryLatencyMs: 5,
    replicas: [{ id: 'r1', latencyMs: 2, replicationLagMs: 100 }],
  });
  const session = new Session();

  const v1 = await writeAndSettle(router, clock, session, 'p1', 'k', 'v1', 5);
  assert.equal(v1, 1);
  assert.equal(session.watermark('p1'), 1);
  await clock.advance(200); // r1 已复制到 v1

  const v2 = await writeAndSettle(router, clock, session, 'p1', 'k', 'v2', 5);
  assert.equal(v2, 1 + 1);
  // 此时 r1 仍停留在 v1，v2 的复制事件将在 100ms 后到达

  let resolved = false;
  const pending = router.read(session, 'p1', 'k', { kind: 'wait' }).then((res) => {
    resolved = true;
    return res;
  });
  await clock.advance(50); // 复制尚未到达，读必须仍在等待
  assert.equal(resolved, false);
  await clock.advance(500); // 复制到达 + 节点处理延迟
  const res = await pending;
  assert.equal(resolved, true);
  assert.equal(res.version, 2);
  assert.equal(res.value, 'v2');
  assert.equal(res.node, 'r1');
  assert.equal(session.watermark('p1'), 2);
});

test('switch 策略：跳过落后副本，切换到已追平的节点', async () => {
  const { clock, cluster, router } = setup();
  cluster.addPartition('p1', {
    primaryLatencyMs: 5,
    replicas: [
      { id: 'fast-but-stale', latencyMs: 1, replicationLagMs: 1000 },
      { id: 'slow-but-fresh', latencyMs: 5, replicationLagMs: 10 },
    ],
  });
  const session = new Session();
  await writeAndSettle(router, clock, session, 'p1', 'k', 'v1', 5);
  await clock.advance(50); // slow-but-fresh 已追平，fast-but-stale 仍落后

  const pending = router.read(session, 'p1', 'k', { kind: 'switch' });
  await clock.advance(50);
  const res = await pending;
  assert.equal(res.node, 'slow-but-fresh');
  assert.equal(res.version, 1);
  assert.equal(res.value, 'v1');
});

test('switch 策略：所有副本都落后时抛 NoFreshReplicaError', async () => {
  const { clock, cluster, router } = setup();
  cluster.addPartition('p1', {
    primaryLatencyMs: 5,
    replicas: [{ id: 'r1', latencyMs: 1, replicationLagMs: 1000 }],
  });
  const session = new Session();
  await writeAndSettle(router, clock, session, 'p1', 'k', 'v1', 5);
  // 不推进时钟，副本仍是 v0，而会话水位已是 v1
  await assert.rejects(router.read(session, 'p1', 'k', { kind: 'switch' }), NoFreshReplicaError);
});

test('timeout 策略：时限内未追平则抛 ReadTimeoutError', async () => {
  const { clock, cluster, router } = setup();
  cluster.addPartition('p1', {
    primaryLatencyMs: 5,
    replicas: [{ id: 'r1', latencyMs: 1, replicationLagMs: 1000 }],
  });
  const session = new Session();
  await writeAndSettle(router, clock, session, 'p1', 'k', 'v1', 5);

  const pending = router.read(session, 'p1', 'k', { kind: 'timeout', timeoutMs: 50 });
  const assertion = assert.rejects(pending, ReadTimeoutError);
  await clock.advance(100); // 超过 50ms 时限，复制仍未到达
  await assertion;
});

test('timeout 策略：时限内追平则正常返回', async () => {
  const { clock, cluster, router } = setup();
  cluster.addPartition('p1', {
    primaryLatencyMs: 5,
    replicas: [{ id: 'r1', latencyMs: 1, replicationLagMs: 100 }],
  });
  const session = new Session();
  await writeAndSettle(router, clock, session, 'p1', 'k', 'v1', 5);

  const pending = router.read(session, 'p1', 'k', { kind: 'timeout', timeoutMs: 500 });
  await clock.advance(500);
  const res = await pending;
  assert.equal(res.version, 1);
  assert.equal(res.value, 'v1');
});

test('单调读不变量：多分区交错读写，任何返回版本都不低于该分区此前返回值', async () => {
  const { clock, cluster, router } = setup();
  const partitions = ['p0', 'p1', 'p2'];
  for (const p of partitions) {
    cluster.addPartition(p, {
      primaryLatencyMs: 2,
      replicas: [
        { id: `${p}-a`, latencyMs: 1, replicationLagMs: 30 },
        { id: `${p}-b`, latencyMs: 3, replicationLagMs: 80 },
      ],
    });
  }
  const session = new Session();
  let seed = 42;
  const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;

  const lastReturned = new Map<string, number>();
  const history = new Map<string, Map<number, unknown>>();
  for (const p of partitions) history.set(p, new Map());

  for (let i = 0; i < 60; i++) {
    const p = partitions[Math.floor(rand() * partitions.length)];
    if (rand() < 0.5) {
      const value = `w${i}`;
      const pending = router.write(session, p, 'k', value);
      await clock.advance(2);
      const version = await pending;
      history.get(p)!.set(version, value);
    } else {
      const pending = router.read(session, p, 'k', { kind: 'wait' });
      await clock.advance(500);
      const res = await pending;
      const prev = lastReturned.get(p) ?? 0;
      assert.ok(
        res.version >= prev,
        `partition ${p}: returned v${res.version} older than previous v${prev}`,
      );
      assert.equal(res.value, history.get(p)!.get(res.version));
      lastReturned.set(p, res.version);
      assert.equal(session.watermark(p), res.version);
    }
  }
});

test('会话合并：每个分区取最大水位', () => {
  const a = new Session();
  a.record('p1', 3);
  a.record('p2', 1);
  const b = new Session();
  b.record('p1', 1);
  b.record('p2', 5);
  b.record('p3', 2);

  const merged = Session.merge(a, b);
  assert.equal(merged.watermark('p1'), 3);
  assert.equal(merged.watermark('p2'), 5);
  assert.equal(merged.watermark('p3'), 2);
  // 合并不修改原会话
  assert.equal(a.watermark('p2'), 1);
  assert.equal(b.watermark('p1'), 1);
});

test('会话 token 往返：水位随 token 传递，读己之写跨请求成立', async () => {
  const { clock, cluster, router } = setup();
  cluster.addPartition('p1', {
    primaryLatencyMs: 5,
    replicas: [{ id: 'r1', latencyMs: 1, replicationLagMs: 1000 }],
  });
  const session = new Session();
  await writeAndSettle(router, clock, session, 'p1', 'k', 'v1', 5);

  const restored = Session.fromToken(session.toToken());
  assert.equal(restored.watermark('p1'), 1);
  // 水位随 token 到达新请求：副本落后时 switch 必须失败而不是返回旧版本
  await assert.rejects(router.read(restored, 'p1', 'k', { kind: 'switch' }), NoFreshReplicaError);
});

test('水位按分区隔离：p1 的高水位不阻塞 p2 的读', async () => {
  const { clock, cluster, router } = setup();
  cluster.addPartition('p1', {
    primaryLatencyMs: 5,
    replicas: [{ id: 'r1', latencyMs: 1, replicationLagMs: 1000 }],
  });
  cluster.addPartition('p2', {
    primaryLatencyMs: 5,
    replicas: [{ id: 'r2', latencyMs: 1, replicationLagMs: 1000 }],
  });
  const session = new Session();
  await writeAndSettle(router, clock, session, 'p1', 'k', 'v1', 5);
  assert.equal(session.watermark('p1'), 1);
  assert.equal(session.watermark('p2'), 0);

  // p2 水位为 0，任何副本都可立即服务，无需等待复制
  const pending = router.read(session, 'p2', 'missing-key', { kind: 'switch' });
  await clock.advance(10);
  const res = await pending;
  assert.equal(res.version, 0);
  assert.equal(res.value, undefined);
});
