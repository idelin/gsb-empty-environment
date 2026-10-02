import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ManualClock } from "../src/clock.ts";
import { RootCoordinator } from "../src/root-coordinator.ts";
import { RegionGateway } from "../src/region-gateway.ts";

function setup(quota = 3) {
  const clock = new ManualClock();
  const root = new RootCoordinator({ clock });
  const gateway = new RegionGateway("region-0", root, { clock, quota });
  gateway.start();
  return { clock, root, gateway };
}

describe("RegionGateway 区域网关", () => {
  it("颁发默认 5s 短周期细粒度子租约", () => {
    const { gateway } = setup();
    const sub = gateway.requestSubLease("worker-1");
    assert.ok(sub);
    assert.equal(sub.expiresAt - sub.issuedAt, 5_000);
    assert.equal(gateway.activeSubLeaseCount(), 1);
  });

  it("子租约总量不得超过根租约的粗粒度配额", () => {
    const { gateway } = setup(2);
    assert.ok(gateway.requestSubLease("w1"));
    assert.ok(gateway.requestSubLease("w2"));
    assert.equal(gateway.requestSubLease("w3"), null);
    assert.equal(gateway.availableQuota(), 0);
  });

  it("心跳续租延长子租约；epoch 不匹配被拒绝", () => {
    const { clock, gateway } = setup();
    const sub = gateway.requestSubLease("w1")!;
    clock.advance(4_000);
    const renewed = gateway.renewSubLease("w1", sub.epoch);
    assert.ok(renewed);
    assert.equal(renewed.expiresAt, 4_000 + 5_000);
    assert.equal(gateway.renewSubLease("w1", sub.epoch), null);
  });

  it("子租约 5s 超时后在本地自动回收配额", () => {
    const { clock, gateway } = setup(1);
    gateway.requestSubLease("w1");
    assert.equal(gateway.availableQuota(), 0);
    clock.advance(5_000);
    assert.equal(gateway.activeSubLeaseCount(), 0);
    assert.equal(gateway.availableQuota(), 1);
    assert.equal(gateway.stats.subLeaseExpirations, 1);
  });

  it("网关在根租约半周期处自动续租，根租约永不间断", () => {
    const { clock, root, gateway } = setup();
    const initial = gateway.currentRootLease()!;
    clock.advance(30_000); // 60s TTL 的 50%
    const renewed = gateway.currentRootLease()!;
    assert.equal(renewed.leaseId, initial.leaseId);
    assert.equal(renewed.epoch, initial.epoch + 1);
    assert.equal(root.stats.renewals, 1);

    clock.advance(300_000); // 再跑 5 分钟
    assert.ok(gateway.currentRootLease()!.expiresAt > clock.now());
    assert.equal(root.activeLeaseCount(), 1);
  });

  it("根租约被撤销后，网关在续租点自动重新申请", () => {
    const { clock, root, gateway } = setup();
    const lease = gateway.currentRootLease()!;
    root.revokeLease(lease.leaseId);

    // 推进到续租点（30s）：续租被根拒绝 -> 网关重新申请新租约
    clock.advance(30_000);
    const reacquired = gateway.currentRootLease()!;
    assert.notEqual(reacquired.leaseId, lease.leaseId);
    assert.ok(gateway.requestSubLease("w-after"));
  });

  it("根租约失效期间不颁发新子租约", () => {
    const { gateway } = setup();
    gateway.stop(); // 释放根租约
    assert.equal(gateway.requestSubLease("w1"), null);
  });
});
