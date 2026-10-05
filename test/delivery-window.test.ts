import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DeliveryWindow,
  type DeliveryEvent,
} from '../src/delivery-window.ts';

function makeWindow(windowSize = 3, timeoutMs = 100): DeliveryWindow {
  return new DeliveryWindow({ windowSize, timeoutMs });
}

function deliverN(w: DeliveryWindow, n: number, at = 0): void {
  for (let i = 0; i < n; i += 1) {
    const r = w.apply({ type: 'deliver', at });
    assert.ok(r.delivered, `expected deliver #${i + 1} to succeed`);
  }
}

test('并发乱序完成时，水位只越过连续完成序号', () => {
  const w = makeWindow(4);
  deliverN(w, 4);

  // 乱序完成：3 先完成，水位不动
  let r = w.apply({ type: 'complete', seq: 3, epoch: 1 });
  assert.equal(r.watermark, 0);
  // 再完成 2，水位仍不动（1 未完成）
  r = w.apply({ type: 'complete', seq: 2, epoch: 1 });
  assert.equal(r.watermark, 0);
  // 完成 1，水位连续越过 1..3
  r = w.apply({ type: 'complete', seq: 1, epoch: 1 });
  assert.equal(r.watermark, 3);
  // 完成 4，水位到 4
  r = w.apply({ type: 'complete', seq: 4, epoch: 1 });
  assert.equal(r.watermark, 4);
  w.checkInvariant();
});

test('重复完成无副作用（幂等）', () => {
  const w = makeWindow(3);
  deliverN(w, 3);

  const first = w.apply({ type: 'complete', seq: 1, epoch: 1 });
  assert.equal(first.watermark, 1);
  assert.equal(first.ignored, null);

  // 重复确认同一序号：被忽略，水位不变，无重投
  const dup = w.apply({ type: 'complete', seq: 1, epoch: 1 });
  assert.equal(dup.watermark, 1);
  assert.equal(dup.ignored, 'already-confirmed');
  assert.deepEqual(dup.redeliveries, []);
  assert.equal(w.snapshot().inFlight, 2);

  // 重复 fail 已确认序号同样无副作用
  const dupFail = w.apply({ type: 'fail', seq: 1, epoch: 1, at: 10 });
  assert.equal(dupFail.ignored, 'already-confirmed');
  assert.deepEqual(dupFail.redeliveries, []);
  w.checkInvariant();
});

test('旧代次确认不能确认新投递', () => {
  const w = makeWindow(2);
  deliverN(w, 2);

  // seq 1 超时重投，代次升为 2
  const t = w.apply({ type: 'timeout', at: 100 });
  assert.deepEqual(t.redeliveries, [
    { seq: 1, epoch: 2 },
    { seq: 2, epoch: 2 },
  ]);

  // 旧代次 (seq=1, epoch=1) 的确认到达：必须被拒绝
  const stale = w.apply({ type: 'complete', seq: 1, epoch: 1 });
  assert.equal(stale.ignored, 'stale-epoch');
  assert.equal(stale.watermark, 0);
  assert.equal(w.snapshot().inFlight, 2);

  // 旧代次 fail 同样无效
  const staleFail = w.apply({ type: 'fail', seq: 1, epoch: 1, at: 200 });
  assert.equal(staleFail.ignored, 'stale-epoch');
  assert.deepEqual(staleFail.redeliveries, []);

  // 新代次确认生效
  const ok = w.apply({ type: 'complete', seq: 1, epoch: 2 });
  assert.equal(ok.watermark, 1);
  w.checkInvariant();
});

test('超时产生重投列表，重投后旧确认失效', () => {
  const w = makeWindow(3, 50);
  deliverN(w, 3, 0);

  // 部分超时前确认 seq 1
  w.apply({ type: 'complete', seq: 1, epoch: 1 });

  // t=50：seq 2、3 超时重投
  const r = w.apply({ type: 'timeout', at: 50 });
  assert.equal(r.watermark, 1);
  assert.deepEqual(r.redeliveries, [
    { seq: 2, epoch: 2 },
    { seq: 3, epoch: 2 },
  ]);

  // 重投前的旧确认（epoch=1）不再生效
  assert.equal(
    w.apply({ type: 'complete', seq: 2, epoch: 1 }).ignored,
    'stale-epoch',
  );
  // 新代次确认生效
  const ok = w.apply({ type: 'complete', seq: 2, epoch: 2 });
  assert.equal(ok.watermark, 2);
  w.checkInvariant();
});

test('失败项立即重投且代次递增', () => {
  const w = makeWindow(2);
  deliverN(w, 2);

  const r = w.apply({ type: 'fail', seq: 2, epoch: 1, at: 30 });
  assert.deepEqual(r.redeliveries, [{ seq: 2, epoch: 2 }]);
  assert.equal(r.watermark, 0);

  // 重投后完成 1、2（新代次），水位连续推进
  w.apply({ type: 'complete', seq: 1, epoch: 1 });
  const done = w.apply({ type: 'complete', seq: 2, epoch: 2 });
  assert.equal(done.watermark, 2);
  w.checkInvariant();
});

test('窗口有界：满窗拒绝投递，水位推进后放行，绝不越界', () => {
  const w = makeWindow(2);
  deliverN(w, 2);

  // 窗口已满（在途 2 = windowSize），继续投递被拒绝
  const blocked = w.apply({ type: 'deliver', at: 10 });
  assert.equal(blocked.delivered, null);
  assert.equal(blocked.ignored, 'window-full');
  assert.equal(w.snapshot().maxIssued, 2);

  // 水位推进到 1 后，窗口滑动，可投递 seq 3
  w.apply({ type: 'complete', seq: 1, epoch: 1 });
  const ok = w.apply({ type: 'deliver', at: 20 });
  assert.deepEqual(ok.delivered, { seq: 3, epoch: 1 });

  // 再次满窗：seq 4 > watermark(1) + 2，被拒绝
  const blockedAgain = w.apply({ type: 'deliver', at: 30 });
  assert.equal(blockedAgain.ignored, 'window-full');
  assert.equal(w.snapshot().maxIssued, 3);
  w.checkInvariant();
});

test('事件流接口：run 返回每步水位与重投列表', () => {
  const w = makeWindow(2, 10);
  const events: DeliveryEvent[] = [
    { type: 'deliver', at: 0 },
    { type: 'deliver', at: 0 },
    { type: 'timeout', at: 10 },
    { type: 'complete', seq: 1, epoch: 2 },
    { type: 'complete', seq: 2, epoch: 2 },
    { type: 'deliver', at: 20 },
  ];
  const results = w.run(events);
  assert.deepEqual(
    results.map((r) => r.watermark),
    [0, 0, 0, 1, 2, 2],
  );
  assert.deepEqual(results[2].redeliveries, [
    { seq: 1, epoch: 2 },
    { seq: 2, epoch: 2 },
  ]);
  assert.deepEqual(results[5].delivered, { seq: 3, epoch: 1 });
  w.checkInvariant();
});

test('随机事件模糊测试：任意交错下窗口不变量始终成立', () => {
  // 简单可复现的 LCG
  let seed = 42;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };

  for (let trial = 0; trial < 50; trial += 1) {
    const windowSize = 1 + Math.floor(rand() * 5);
    const w = makeWindow(windowSize, 20);
    let now = 0;
    let maxCompletedWatermark = 0;

    for (let step = 0; step < 500; step += 1) {
      now += Math.floor(rand() * 15);
      const snap = w.snapshot();
      const pick = rand();
      let event: DeliveryEvent;
      if (pick < 0.4 || snap.inFlight === 0) {
        event = { type: 'deliver', at: now };
      } else if (pick < 0.75) {
        const targets = snap.outstanding;
        const t = targets[Math.floor(rand() * targets.length)];
        // 有一半概率使用错误代次，模拟旧确认
        const epoch = rand() < 0.5 ? t.epoch : t.epoch - 1;
        event = { type: 'complete', seq: t.seq, epoch };
      } else if (pick < 0.9) {
        const targets = snap.outstanding;
        const t = targets[Math.floor(rand() * targets.length)];
        event = { type: 'fail', seq: t.seq, epoch: t.epoch, at: now };
      } else {
        event = { type: 'timeout', at: now };
      }

      const r = w.apply(event);

      // 水位单调不减
      assert.ok(r.watermark >= maxCompletedWatermark);
      maxCompletedWatermark = r.watermark;
      // 重投项代次必须大于 1（确实发生过重投）
      for (const rd of r.redeliveries) {
        assert.ok(rd.epoch >= 2);
      }
      // 每一步都验证窗口不越界
      w.checkInvariant();

      const after = w.snapshot();
      assert.ok(after.inFlight <= windowSize);
      for (const o of after.outstanding) {
        assert.ok(o.seq > after.watermark);
        assert.ok(o.seq <= after.watermark + windowSize);
      }
    }
  }
});

test('参数校验', () => {
  assert.throws(() => new DeliveryWindow({ windowSize: 0, timeoutMs: 10 }));
  assert.throws(() => new DeliveryWindow({ windowSize: 2, timeoutMs: 0 }));
});
