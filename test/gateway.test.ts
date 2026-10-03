import { test } from "node:test";
import assert from "node:assert/strict";
import { HierarchicalGateway } from "../src/gateway.ts";

function buildGateway(): HierarchicalGateway {
  const gw = new HierarchicalGateway();
  // 第一层：集群网关节点
  for (const id of ["gw-east-1", "gw-east-2", "gw-west-1"]) {
    gw.addClusterGateway(id);
  }
  // 第二层：各租户独立的协议处理节点环
  for (const id of ["t100-p1", "t100-p2", "t100-p3"]) gw.addProtocolNode("100", id);
  for (const id of ["t200-p1", "t200-p2"]) gw.addProtocolNode("200", id);
  for (const id of ["t300-p1", "t300-p2"]) gw.addProtocolNode("300", id);
  return gw;
}

test("验收：Tenant=100, Device=ABC 在两层环中级联穿透", () => {
  const gw = buildGateway();
  const result = gw.route("100", "ABC");

  assert.equal(result.path.length, 2, "必须级联穿透两层");
  assert.equal(result.path[0].layer, "cluster");
  assert.equal(result.path[0].ringKey, "tenant:100");
  assert.ok(gw.clusterGateways.includes(result.clusterGateway));
  assert.equal(result.path[1].layer, "tenant");
  assert.equal(result.path[1].ringKey, "device:ABC");
  assert.ok(gw.protocolNodesOf("100").includes(result.protocolNode));
  assert.equal(result.path[0].nodeId, result.clusterGateway);
  assert.equal(result.path[1].nodeId, result.protocolNode);

  // 路由确定性：同一请求多次路由结果一致
  for (let i = 0; i < 50; i++) {
    assert.deepEqual(gw.route("100", "ABC"), result);
  }
});

test("验收：单租户扩容不影响其他租户的哈希映射分布", () => {
  const gw = buildGateway();

  const devices = Array.from({ length: 5000 }, (_, i) => `DEV-${i}`);
  const snapshotOf = (tenant: string): Map<string, string> =>
    new Map(devices.map((d) => [d, gw.routeProtocolNode(tenant, d)]));

  const t200Before = snapshotOf("200");
  const t300Before = snapshotOf("300");
  const t100Before = snapshotOf("100");

  // 租户 100 单租户扩容：新增 2 个协议处理节点
  gw.addProtocolNode("100", "t100-p4");
  gw.addProtocolNode("100", "t100-p5");

  // 其他租户映射逐 key 完全一致
  const t200After = snapshotOf("200");
  const t300After = snapshotOf("300");
  for (const d of devices) {
    assert.equal(t200After.get(d), t200Before.get(d), `租户200 设备 ${d} 映射被扰动`);
    assert.equal(t300After.get(d), t300Before.get(d), `租户300 设备 ${d} 映射被扰动`);
  }

  // 租户 100 自身仅部分 key 迁移，且只能迁往新节点
  let moved = 0;
  for (const d of devices) {
    const after = gw.routeProtocolNode("100", d);
    if (after !== t100Before.get(d)) {
      moved++;
      assert.ok(after === "t100-p4" || after === "t100-p5", "迁移的 key 只能流向新节点");
    }
  }
  const ratio = moved / devices.length;
  assert.ok(ratio > 0.1 && ratio < 0.7, `租户100 迁移比例应接近 2/5，实际 ${(ratio * 100).toFixed(1)}%`);
});

test("两层独立扩缩容：第一层扩容不影响第二层映射", () => {
  const gw = buildGateway();
  const devices = Array.from({ length: 2000 }, (_, i) => `DEV-${i}`);
  const before = new Map(devices.map((d) => [d, gw.routeProtocolNode("100", d)]));

  gw.addClusterGateway("gw-north-1");
  for (const d of devices) {
    assert.equal(gw.routeProtocolNode("100", d), before.get(d));
  }
  // 第一层仍可将 Tenant=100 路由到某个集群网关
  assert.ok(gw.clusterGateways.includes(gw.routeCluster("100")));
});

test("容灾：摘除协议节点后仅受影响 key 迁移，请求仍可路由", () => {
  const gw = buildGateway();
  const devices = Array.from({ length: 5000 }, (_, i) => `DEV-${i}`);
  const before = new Map(devices.map((d) => [d, gw.routeProtocolNode("100", d)]));

  const victim = gw.route("100", "ABC").protocolNode;
  assert.equal(gw.removeProtocolNode("100", victim), true);

  for (const d of devices) {
    const prev = before.get(d)!;
    const after = gw.routeProtocolNode("100", d);
    if (prev !== victim) {
      assert.equal(after, prev, "未受影响 key 不得迁移");
    } else {
      assert.notEqual(after, victim);
    }
  }
  // 摘除后 Tenant=100/Device=ABC 依然可完成双层级联路由
  const rerouted = gw.route("100", "ABC");
  assert.notEqual(rerouted.protocolNode, victim);
  assert.equal(rerouted.path.length, 2);
});

test("容灾：摘除集群网关节点后第一层仍可路由", () => {
  const gw = buildGateway();
  const hit = gw.routeCluster("100");
  assert.equal(gw.removeClusterGateway(hit), true);
  const after = gw.routeCluster("100");
  assert.notEqual(after, hit);
  assert.ok(gw.clusterGateways.includes(after));
});

test("无协议节点的租户在路由时报错", () => {
  const gw = buildGateway();
  assert.throws(() => gw.route("999", "ABC"), /no protocol nodes/);
});
