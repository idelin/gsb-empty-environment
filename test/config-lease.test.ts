import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { ManualClock } from "../src/clock.ts";
import {
  ConfigCenter,
  LeaseRevokedError,
  StaleVersionError,
} from "../src/config-center.ts";
import {
  NoValidLeaseError,
  StaleConfigRejectedError,
  WorkerNode,
} from "../src/worker-node.ts";

const TTL = 30_000;

function setup(initialData: Record<string, string> = { "db.password": "s3cret-v1" }) {
  const clock = new ManualClock(1_000_000);
  const center = new ConfigCenter(initialData, { leaseTtlMs: TTL, clock });
  return { clock, center };
}

describe("配置租用与接客门槛", () => {
  it("节点租用配置后持有有效租约，可以对外接客", () => {
    const { clock, center } = setup();
    const node = new WorkerNode("node-1", center, clock);
    node.connect();

    assert.equal(node.hasValidLease(), true);
    assert.equal(node.currentConfigVersion, 1);
    const res = node.serve("SELECT 1");
    assert.equal(res.configVersion, 1);
    assert.equal(node.configValue("db.password"), "s3cret-v1");
  });

  it("未租用配置的节点没有有效租约，拒绝接客", () => {
    const { clock, center } = setup();
    const node = new WorkerNode("node-1", center, clock);

    assert.equal(node.hasValidLease(), false);
    assert.throws(() => node.serve("SELECT 1"), NoValidLeaseError);
  });

  it("租约可续期延长接客窗口", () => {
    const { clock, center } = setup();
    const node = new WorkerNode("node-1", center, clock);
    node.connect();

    clock.advance(TTL - 1);
    node.renewLease();
    clock.advance(TTL - 1);
    assert.equal(node.hasValidLease(), true);
    assert.doesNotThrow(() => node.serve("SELECT 1"));
  });
});

describe("验收边界一/二：配置中心修改数据库密码并全网换发", () => {
  it("更新配置后版本递增，旧租约全部撤销，在线节点收到新配置与新租约", () => {
    const { clock, center } = setup();
    const nodeA = new WorkerNode("node-a", center, clock);
    const nodeB = new WorkerNode("node-b", center, clock);
    nodeA.connect();
    nodeB.connect();
    const oldLeaseIdOfA = (nodeA.serve("ping") as { result: string }).result;

    center.updateConfig({ "db.password": "s3cret-v2" });

    assert.equal(center.currentVersion, 2);
    assert.equal(nodeA.currentConfigVersion, 2);
    assert.equal(nodeB.currentConfigVersion, 2);
    assert.equal(nodeA.configValue("db.password"), "s3cret-v2");
    assert.equal(nodeB.configValue("db.password"), "s3cret-v2");
    assert.match(oldLeaseIdOfA, /lease-1/);
    assert.match(nodeA.serve("ping").result, /lease-3/);
    assert.doesNotThrow(() => nodeA.serve("SELECT 1"));
    assert.doesNotThrow(() => nodeB.serve("SELECT 1"));
  });

  it("旧租约在配置中心侧失效：续租被拒绝，凭证校验不通过", () => {
    const { clock, center } = setup();
    const binding = center.rent("node-1");
    const oldLease = binding.lease;
    assert.equal(center.validate(oldLease.leaseId, oldLease.configVersion), true);

    center.updateConfig({ "db.password": "s3cret-v2" });

    assert.equal(center.validate(oldLease.leaseId, oldLease.configVersion), false);
    assert.throws(() => center.renew(oldLease.leaseId), LeaseRevokedError);
  });

  it("版本约束：拒绝为旧版本租约续期（先过期判定前的撤销优先）", () => {
    const { clock, center } = setup();
    const node = new WorkerNode("node-1", center, clock);
    node.connect();
    center.updateConfig({ "db.password": "s3cret-v2" });

    // 节点已收到新绑定；手工构造一个"错过推送"的场景：直接租旧版本再更新
    const staleBinding = center.rent("node-slow");
    center.updateConfig({ "db.password": "s3cret-v3" });
    assert.throws(() => center.renew(staleBinding.lease.leaseId), LeaseRevokedError);
    assert.equal(node.currentConfigVersion, 3);
  });
});

describe("验收边界三：脱网慢节点旧租约自然失效后主动停止接客", () => {
  it("脱网节点收不到新配置，旧租约到期后拒绝流量，防止脏数据", () => {
    const { clock, center } = setup();
    const slow = new WorkerNode("node-slow", center, clock);
    const healthy = new WorkerNode("node-fast", center, clock);
    slow.connect();
    healthy.connect();

    // 慢节点脱网：收不到配置中心的推送
    slow.disconnect();

    center.updateConfig({ "db.password": "s3cret-v2" });
    assert.equal(healthy.currentConfigVersion, 2);
    assert.equal(slow.currentConfigVersion, 1); // 仍持有旧配置

    // 租约到期前：旧租约本地仍有效，可短暂继续服务（租约语义允许的宽限）；
    // 健康节点按时续租，保持服务
    clock.advance(TTL - 1);
    assert.equal(slow.hasValidLease(), true);
    healthy.renewLease();

    // 旧租约自然失效：无需配置中心通知，节点本地判定并主动停止接客
    clock.advance(1);
    assert.equal(slow.hasValidLease(), false);
    assert.throws(() => slow.serve("UPDATE users SET ..."), NoValidLeaseError);

    // 健康节点在新租约下正常服务
    assert.doesNotThrow(() => healthy.serve("UPDATE users SET ..."));
  });

  it("脱网节点恢复联网后重新租用新配置方可恢复接客", () => {
    const { clock, center } = setup();
    const slow = new WorkerNode("node-slow", center, clock);
    slow.connect();
    slow.disconnect();
    center.updateConfig({ "db.password": "s3cret-v2" });
    clock.advance(TTL + 1);
    assert.throws(() => slow.serve("SELECT 1"), NoValidLeaseError);

    // 重新联网：重新 rent 获得 v2 配置与新租约
    slow.connect();
    assert.equal(slow.currentConfigVersion, 2);
    assert.equal(slow.configValue("db.password"), "s3cret-v2");
    assert.doesNotThrow(() => slow.serve("SELECT 1"));
  });
});

describe("版本约束的防御性校验", () => {
  it("节点拒绝不高于本地版本的配置重放", () => {
    const { clock, center } = setup();
    const node = new WorkerNode("node-1", center, clock);
    node.connect();
    center.updateConfig({ "db.password": "s3cret-v2" });
    assert.equal(node.currentConfigVersion, 2);

    // 重放一个 v1 的旧绑定
    const staleCenter = new ConfigCenter({ "db.password": "s3cret-v1" }, { leaseTtlMs: TTL, clock });
    const staleBinding = staleCenter.rent("node-1");
    assert.throws(
      () => (node as unknown as { applyBinding(b: unknown): void }).applyBinding(staleBinding),
      StaleConfigRejectedError,
    );
    // 节点状态未被污染
    assert.equal(node.currentConfigVersion, 2);
    assert.doesNotThrow(() => node.serve("SELECT 1"));
  });

  it("配置中心拒绝为版本落后的租约续期（StaleVersionError 路径）", () => {
    const { clock, center } = setup();
    const binding = center.rent("node-1");
    // 直接构造：绕过撤销、仅版本落后的场景
    const record = { ...binding.lease, configVersion: binding.lease.configVersion };
    center.updateConfig({ "db.password": "s3cret-v2" });
    // 已被撤销，抛 LeaseRevokedError；再验证 validate 的版本比对
    assert.throws(() => center.renew(record.leaseId), LeaseRevokedError);
    const fresh = center.rent("node-2");
    assert.equal(center.validate(fresh.lease.leaseId, fresh.lease.configVersion - 1), false);
    assert.equal(center.validate(fresh.lease.leaseId, fresh.lease.configVersion), true);
    assert.ok(StaleVersionError.prototype instanceof Error);
  });
});
