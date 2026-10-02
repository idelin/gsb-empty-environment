import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ManualClock } from "../src/clock.ts";
import { LeaseBinder } from "../src/lease-binder.ts";
import { ConfigCenter } from "../src/config-center.ts";
import { NoValidLeaseError, WorkerNode } from "../src/worker-node.ts";

const TTL = 1000;
const MARGIN = 200;

function makeCluster() {
  const clock = new ManualClock(0);
  const center = new ConfigCenter({
    leaseTtlMs: TTL,
    clock,
    initialConfig: { dbPassword: "pw-v1" },
  });
  const makeNode = (nodeId: string) =>
    new WorkerNode(nodeId, center, { renewalMarginMs: MARGIN, clock });
  return { clock, center, makeNode };
}

describe("LeaseBinder 租约绑定与版本约束", () => {
  it("签发后可校验通过，版本不符被拒绝", () => {
    const clock = new ManualClock(0);
    const binder = new LeaseBinder({ ttlMs: TTL, clock });
    const lease = binder.issue("node-1", 1);

    assert.equal(binder.validate(lease.leaseId, 1).ok, true);
    const mismatch = binder.validate(lease.leaseId, 2);
    assert.equal(mismatch.ok, false);
    assert.equal(mismatch.ok === false && mismatch.reason, "version-mismatch");
  });

  it("TTL 到期后租约自然失效", () => {
    const clock = new ManualClock(0);
    const binder = new LeaseBinder({ ttlMs: TTL, clock });
    const lease = binder.issue("node-1", 1);

    clock.advance(TTL);
    const result = binder.validate(lease.leaseId, 1);
    assert.equal(result.ok, false);
    assert.equal(result.ok === false && result.reason, "expired");
  });

  it("撤销后租约立即失效，revokeVersion 批量撤销", () => {
    const clock = new ManualClock(0);
    const binder = new LeaseBinder({ ttlMs: TTL, clock });
    const l1 = binder.issue("node-1", 1);
    const l2 = binder.issue("node-2", 1);
    const l3 = binder.issue("node-3", 2);

    assert.equal(binder.revokeVersion(1), 2);
    assert.equal(binder.validate(l1.leaseId, 1).ok, false);
    assert.equal(binder.validate(l2.leaseId, 1).ok, false);
    assert.equal(binder.validate(l3.leaseId, 2).ok, true);
  });

  it("续租延长 TTL，但旧版本租约不可续", () => {
    const clock = new ManualClock(0);
    const binder = new LeaseBinder({ ttlMs: TTL, clock });
    const lease = binder.issue("node-1", 1);

    clock.advance(TTL - 1);
    const renewed = binder.renew(lease.leaseId, 1);
    assert.equal(renewed.ok, true);
    clock.advance(TTL - 1);
    assert.equal(binder.validate(lease.leaseId, 1).ok, true);

    const rejected = binder.renew(lease.leaseId, 2);
    assert.equal(rejected.ok, false);
    assert.equal(rejected.ok === false && rejected.reason, "version-mismatch");
  });
});

describe("工作节点：持有有效租约才接客", () => {
  it("未租用配置前拒绝流量", () => {
    const { makeNode } = makeCluster();
    const node = makeNode("node-1");
    assert.throws(() => node.serve("req-1"), NoValidLeaseError);
  });

  it("上线后持 v1 租约对外服务", () => {
    const { center, makeNode } = makeCluster();
    const node = makeNode("node-1");
    node.start();
    const res = node.serve("req-1");
    assert.equal(res.configVersion, 1);
    assert.equal(res.dbPassword, "pw-v1");
    assert.equal(center.validateLease((node as any).lease.leaseId).ok, true);
  });

  it("临期自动续租，持续持有有效租约", () => {
    const { clock, makeNode } = makeCluster();
    const node = makeNode("node-1");
    node.start();
    for (let i = 0; i < 5; i++) {
      clock.advance(TTL - MARGIN);
      node.maintain();
      assert.equal(node.isServing, true);
    }
    assert.doesNotThrow(() => node.serve("req-1"));
  });
});

describe("验收：配置中心修改数据库密码并全量发布", () => {
  it("收回旧租约，在线节点切换到新配置与新租约", () => {
    const { center, makeNode } = makeCluster();
    const nodeA = makeNode("node-a");
    const nodeB = makeNode("node-b");
    nodeA.start();
    nodeB.start();
    const oldLeaseIdA = (nodeA as any).lease.leaseId as string;

    const result = center.updateConfig({ dbPassword: "pw-v2" });

    assert.equal(result.version, 2);
    assert.equal(result.revokedLeases, 2);
    assert.deepEqual(result.delivered.sort(), ["node-a", "node-b"]);

    const oldValidation = center.validateLease(oldLeaseIdA);
    assert.equal(oldValidation.ok, false);
    assert.equal(oldValidation.ok === false && oldValidation.reason, "revoked");

    for (const node of [nodeA, nodeB]) {
      const res = node.serve("req");
      assert.equal(res.configVersion, 2);
      assert.equal(res.dbPassword, "pw-v2");
    }
  });

  it("脱网慢节点：旧租约自然失效后主动停止接收流量", () => {
    const { clock, center, makeNode } = makeCluster();
    const fast = makeNode("node-fast");
    const slow = makeNode("node-slow");
    fast.start();
    slow.start();
    const staleLeaseId = (slow as any).lease.leaseId as string;

    // 慢节点脱网：收不到推送，也无法续租
    slow.disconnect();
    const result = center.updateConfig({ dbPassword: "pw-v2" });
    assert.deepEqual(result.delivered, ["node-fast"]);

    // 中心视角：旧租约已撤销，任何携带旧租约的操作都会被拒绝
    const centerView = center.validateLease(staleLeaseId);
    assert.equal(centerView.ok, false);
    assert.equal(centerView.ok === false && centerView.reason, "revoked");

    // 慢节点本地 TTL 未到期前仍持有旧租约（租约机制的固有窗口）
    assert.equal(slow.hasValidLease(), true);

    // TTL 到期 → 旧租约自然失效 → 主动停止接客，不再用过期配置操作脏数据
    clock.advance(TTL - MARGIN);
    fast.maintain(); // 在线节点正常续租
    clock.advance(MARGIN);
    slow.maintain();
    assert.equal(slow.isServing, false);
    assert.throws(() => slow.serve("req-late"), NoValidLeaseError);

    // 在线节点全程使用新配置
    assert.equal(fast.serve("req").dbPassword, "pw-v2");
  });

  it("脱网节点重连后续租被拒（版本过旧），立即停止接客", () => {
    const { clock, center, makeNode } = makeCluster();
    const node = makeNode("node-lagging");
    node.start();

    node.disconnect();
    center.updateConfig({ dbPassword: "pw-v2" });

    // 重连时旧租约尚未自然到期，但版本已落后
    clock.advance(TTL - MARGIN);
    node.reconnect();
    assert.equal(node.isServing, false);
    assert.throws(() => node.serve("req"), NoValidLeaseError);
  });
});
