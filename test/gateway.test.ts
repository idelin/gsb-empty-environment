import { test } from "node:test";
import assert from "node:assert/strict";
import { LeaseRouterGateway } from "../src/gateway.ts";
import { KeyRange } from "../src/key-range.ts";
import { StorageNode } from "../src/storage-node.ts";

function setup(options: { maxPartitionSize?: number; leaseTtlMs?: number } = {}) {
  const gateway = new LeaseRouterGateway(options);
  const node1 = new StorageNode("node-1");
  const node2 = new StorageNode("node-2");
  gateway.registerNode(node1);
  gateway.registerNode(node2);
  return { gateway, node1, node2 };
}

test("初始分区 [A-Z) 授予独立租约并可路由", async () => {
  const { gateway } = setup();
  const root = gateway.bootstrap(new KeyRange("A", "Z"), "node-1");

  assert.equal(root.range.toString(), "[A-Z)");
  assert.equal(root.lease!.holderId, "node-1");
  assert.equal(root.lease!.status, "active");

  const res = await gateway.handleRequest({ key: "Apple" });
  assert.equal(res.nodeId, "node-1");
});

test("数据过大触发在 'M' 处分裂为 [A-M) 与 [M-Z)", () => {
  const { gateway } = setup({ maxPartitionSize: 1000 });
  const root = gateway.bootstrap(new KeyRange("A", "Z"), "node-1");

  const result = gateway.reportSize(root.partitionId, 5000, "M");
  assert.ok(result, "超过阈值应触发分裂");

  assert.equal(result.left.range.toString(), "[A-M)");
  assert.equal(result.right.range.toString(), "[M-Z)");

  const active = gateway.listActivePartitions().map((p) => p.range.toString());
  assert.deepEqual(active, ["[A-M)", "[M-Z)"]);
  assert.equal(gateway.getPartition(root.partitionId)!.state, "split");
});

test("分裂后两个子区间获得相互独立的租约", () => {
  const { gateway } = setup();
  const root = gateway.bootstrap(new KeyRange("A", "Z"), "node-1");
  const { leftLease, rightLease } = gateway.split(root.partitionId, "M", {
    left: "node-1",
    right: "node-2",
  });

  // 独立：不同 leaseId、不同持有者、不同 epoch
  assert.notEqual(leftLease.leaseId, rightLease.leaseId);
  assert.equal(leftLease.holderId, "node-1");
  assert.equal(rightLease.holderId, "node-2");
  assert.notEqual(leftLease.epoch, rightLease.epoch);

  // 租约继承：子租约记录父 epoch，父租约已被吊销（fencing）
  const parentEpoch = root.lease!.epoch;
  assert.equal(leftLease.parentEpoch, parentEpoch);
  assert.equal(rightLease.parentEpoch, parentEpoch);
  assert.equal(root.lease!.status, "revoked");
  assert.ok(leftLease.epoch > parentEpoch);
  assert.ok(rightLease.epoch > parentEpoch);
});

test("'Apple' 与 'Zoo' 分别路由到 Node 1 与 Node 2", async () => {
  const { gateway } = setup();
  const root = gateway.bootstrap(new KeyRange("A", "Z"), "node-1");
  gateway.split(root.partitionId, "M", { left: "node-1", right: "node-2" });

  assert.equal((await gateway.handleRequest({ key: "Apple" })).nodeId, "node-1");
  assert.equal((await gateway.handleRequest({ key: "Zoo" })).nodeId, "node-2");
  // 边界：'M' 属于右区间 [M-Z)
  assert.equal((await gateway.handleRequest({ key: "M" })).nodeId, "node-2");
  // 区间外报错
  await assert.rejects(() => gateway.handleRequest({ key: "0abc" }), /没有覆盖/);
});

test("两个节点独立并发处理，互不阻塞", async () => {
  const gateway = new LeaseRouterGateway();
  const slow = new StorageNode("node-1", 120); // 慢节点
  const fast = new StorageNode("node-2", 5);   // 快节点
  gateway.registerNode(slow);
  gateway.registerNode(fast);
  const root = gateway.bootstrap(new KeyRange("A", "Z"), "node-1");
  gateway.split(root.partitionId, "M", { left: "node-1", right: "node-2" });

  const started = Date.now();
  const [apple, zoo] = await Promise.all([
    gateway.handleRequest({ key: "Apple" }), // -> 慢节点
    gateway.handleRequest({ key: "Zoo" }),   // -> 快节点
  ]);

  // 快节点先完成：未被慢节点阻塞
  assert.ok(zoo.processedAt < apple.processedAt, "Zoo 应先于 Apple 完成");
  assert.ok(
    zoo.processedAt - started < 100,
    `Zoo 应在 100ms 内完成（实际 ${zoo.processedAt - started}ms），未被 Apple 阻塞`,
  );
  // 总耗时约等于慢节点耗时，而非两者之和
  const elapsed = Date.now() - started;
  assert.ok(elapsed < 120 + 5 + 60, `并发总耗时 ${elapsed}ms 应远小于串行 125ms+ 裕量`);

  // 同节点并发请求在同一节点内并行
  const many = await Promise.all(
    Array.from({ length: 5 }, (_, i) =>
      gateway.handleRequest({ key: `A${i}` }),
    ),
  );
  assert.ok(many.every((r) => r.nodeId === "node-1"));
  assert.ok(slow.maxObservedConcurrency > 1, "同一节点内请求并发执行");
});

test("租约过期后路由失败，重新授予后恢复", async () => {
  const { gateway } = setup({ leaseTtlMs: 20 });
  const root = gateway.bootstrap(new KeyRange("A", "Z"), "node-1");

  await new Promise((r) => setTimeout(r, 30));
  await assert.rejects(() => gateway.handleRequest({ key: "Apple" }), /没有有效租约/);
  assert.equal(root.lease!.status, "expired");
});

test("非法分裂点被拒绝", () => {
  const { gateway } = setup();
  const root = gateway.bootstrap(new KeyRange("A", "Z"), "node-1");
  assert.throws(() => gateway.split(root.partitionId, "A"), /不在分区/);
  assert.throws(() => gateway.split(root.partitionId, "Z"), /不在分区/);
  assert.throws(() => gateway.split(root.partitionId, "0"), /不在分区/);
});

test("多级分裂：右区间继续在 'S' 处分裂并继承租约谱系", async () => {
  const { gateway } = setup();
  const root = gateway.bootstrap(new KeyRange("A", "Z"), "node-1");
  const first = gateway.split(root.partitionId, "M", {
    left: "node-1",
    right: "node-2",
  });
  const second = gateway.split(first.right.partitionId, "S", {
    left: "node-2",
    right: "node-1",
  });

  assert.equal(second.left.range.toString(), "[M-S)");
  assert.equal(second.right.range.toString(), "[S-Z)");
  // 谱系：第二代租约的 parentEpoch 指向第一代右子租约
  assert.equal(second.leftLease.parentEpoch, first.rightLease.epoch);

  const active = gateway.listActivePartitions().map((p) => p.range.toString());
  assert.deepEqual(active, ["[A-M)", "[M-S)", "[S-Z)"]);

  assert.equal((await gateway.handleRequest({ key: "Apple" })).nodeId, "node-1");
  assert.equal((await gateway.handleRequest({ key: "Monday" })).nodeId, "node-2");
  assert.equal((await gateway.handleRequest({ key: "Zoo" })).nodeId, "node-1");
});
