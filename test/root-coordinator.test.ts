import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ManualClock } from "../src/clock.ts";
import { RootCoordinator } from "../src/root-coordinator.ts";

describe("RootCoordinator 根协调器", () => {
  it("颁发默认 60s 长周期粗粒度租约", () => {
    const clock = new ManualClock();
    const root = new RootCoordinator({ clock });
    const lease = root.requestLease("region-0", 200);
    assert.equal(lease.regionId, "region-0");
    assert.equal(lease.quota, 200);
    assert.equal(lease.expiresAt - lease.issuedAt, 60_000);
    assert.equal(root.activeLeaseCount(), 1);
  });

  it("续租延长 TTL 并递增 epoch；旧 epoch 续租被拒绝", () => {
    const clock = new ManualClock();
    const root = new RootCoordinator({ clock });
    const lease = root.requestLease("region-0", 200);

    clock.advance(30_000);
    const renewed = root.renewLease(lease.leaseId, lease.epoch);
    assert.ok(renewed);
    assert.equal(renewed.epoch, 2);
    assert.equal(renewed.expiresAt, 30_000 + 60_000);

    // 用旧 epoch 续租（如延迟到达的重复请求）必须失败
    assert.equal(root.renewLease(lease.leaseId, 1), null);
  });

  it("过期租约不可续租，撤销后立即失效", () => {
    const clock = new ManualClock();
    const root = new RootCoordinator({ clock });
    const lease = root.requestLease("region-0", 200);

    clock.advance(61_000);
    assert.equal(root.renewLease(lease.leaseId, lease.epoch), null);
    assert.equal(root.activeLeaseCount(), 0);

    const lease2 = root.requestLease("region-1", 100);
    assert.equal(root.revokeLease(lease2.leaseId), true);
    assert.equal(root.activeLeaseCount(), 0);
    assert.equal(root.renewLease(lease2.leaseId, lease2.epoch), null);
  });

  it("统计每次消息处理，支持时间窗口负载探针", () => {
    const clock = new ManualClock();
    const root = new RootCoordinator({ clock });
    root.requestLease("region-0", 10);
    clock.advance(10_000);
    root.requestLease("region-1", 10);
    root.requestLease("region-2", 10);

    assert.equal(root.stats.messagesProcessed, 3);
    assert.equal(root.messagesInWindow(0, 5_000), 1);
    assert.equal(root.messagesInWindow(5_000, 20_000), 2);
  });
});
