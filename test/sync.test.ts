import assert from "node:assert/strict";
import { test } from "node:test";
import { SyncNode, packetBytes, type Packet } from "../src/sync-node.ts";

function buildDoc(id: string, size: number): SyncNode {
  const node = new SyncNode(id);
  let tail: string | null = null;
  for (let i = 0; i < size; i++) tail = node.doc.insertAfter(tail, "x");
  return node;
}

function bootstrap(from: SyncNode, to: SyncNode): void {
  const ack = to.receive(from.statePacketFor(to.id));
  if (ack) from.receive(ack);
}

function syncOnce(from: SyncNode, to: SyncNode): void {
  const packet = from.deltaPacketFor(to.id);
  if (!packet) return;
  const ack = to.receive(packet);
  if (ack) from.receive(ack);
}

function fullSync(a: SyncNode, b: SyncNode): void {
  for (let i = 0; i < 3; i++) {
    syncOnce(a, b);
    syncOnce(b, a);
  }
}

function opsOf(packet: Packet): unknown[] {
  assert.equal(packet.kind, "delta");
  return (packet as { ops: unknown[] }).ops;
}

test("验收：10000 节点文档修改 1 字符，Delta 包为常数级字节", () => {
  const sizes = [1_000, 10_000];
  const deltaSizes: number[] = [];
  const fullSizes: number[] = [];
  for (const size of sizes) {
    const a = buildDoc("A", size);
    const b = new SyncNode("B");
    bootstrap(a, b);
    fullSizes.push(packetBytes(a.statePacketFor("B")));
    a.insertAt(size / 2, "Z");
    const delta = a.deltaPacketFor("B");
    assert.ok(delta, "应有增量包");
    assert.equal(opsOf(delta).length, 1, "Delta 仅含 1 个操作");
    deltaSizes.push(packetBytes(delta));
  }
  for (const bytes of deltaSizes) {
    assert.ok(bytes < 256, `Delta 包 ${bytes} 字节应为常数级 (<256)`);
  }
  assert.ok(
    Math.abs(deltaSizes[1] - deltaSizes[0]) <= 4,
    `Delta 大小不随文档规模增长: ${deltaSizes[0]}B vs ${deltaSizes[1]}B`,
  );
  assert.ok(fullSizes[1] > fullSizes[0] * 5, "全量状态包随文档规模线性增长");
});

test("验收：接收端应用 Delta 后与全量传输完全等价", () => {
  const a = buildDoc("A", 10_000);
  const b = new SyncNode("B");
  const c = new SyncNode("C");
  bootstrap(a, b);
  bootstrap(a, c);

  a.insertAt(123, "中");
  a.insertAt(9_999, "!");
  a.deleteAt(0);

  syncOnce(a, b); // B 走增量 Delta
  const ack = c.receive(a.statePacketFor("C")); // C 走全量 State
  if (ack) a.receive(ack);

  assert.equal(b.text(), a.text(), "Delta 同步后文本一致");
  assert.equal(c.text(), a.text(), "全量传输后文本一致");
  assert.equal(b.text(), c.text(), "两条路径结果完全等价");
  assert.deepEqual(b.doc.version, a.doc.version, "版本向量一致");
});

test("确认水位：仅生成并发送自上次确认后的变动", () => {
  const a = buildDoc("A", 100);
  const b = new SyncNode("B");
  bootstrap(a, b);

  assert.equal(a.deltaPacketFor("B"), null, "无变动时不产生流量");

  a.insertAt(50, "X");
  const d1 = a.deltaPacketFor("B");
  assert.ok(d1);
  assert.equal(opsOf(d1).length, 1);
  const ack = b.receive(d1);
  assert.ok(ack);
  a.receive(ack!);

  assert.equal(a.deltaPacketFor("B"), null, "已确认的变动不重复发送");
  assert.deepEqual(a.watermarkFor("B"), a.doc.version, "水位推进到最新");

  a.insertAt(51, "Y");
  a.insertAt(52, "Z");
  const d2 = a.deltaPacketFor("B");
  assert.ok(d2);
  assert.equal(opsOf(d2).length, 2, "仅含上次确认后的新变动");
});

test("并发编辑双向同步后收敛", () => {
  const a = buildDoc("A", 100);
  const b = new SyncNode("B");
  bootstrap(a, b);

  a.insertAt(0, "A");
  a.deleteAt(50);
  b.insertAt(0, "B");
  b.deleteAt(10);

  fullSync(a, b);
  assert.equal(a.text(), b.text(), "并发编辑后文本收敛");
  assert.deepEqual(a.doc.version, b.doc.version, "版本向量收敛");
});

test("Delta 乱序与重复投递仍收敛（幂等）", () => {
  const a = buildDoc("A", 10);
  const b = new SyncNode("B");
  bootstrap(a, b);

  a.insertAt(5, "1");
  a.insertAt(6, "2");
  a.insertAt(7, "3");
  const delta = a.deltaPacketFor("B")!;
  const ops = opsOf(delta) as never[];
  assert.equal(ops.length, 3);

  // 乱序：先交付后两个操作，再交付第一个
  b.receive({ kind: "delta", from: "A", to: "B", base: {}, ops: ops.slice(1) });
  b.receive({ kind: "delta", from: "A", to: "B", base: {}, ops: ops.slice(0, 1) });
  // 重复投递完整包
  b.receive(delta);
  b.receive(delta);

  assert.equal(b.text(), a.text());
  assert.deepEqual(b.doc.version, a.doc.version);
});

test("删除语义通过 Delta 正确传播（墓碑）", () => {
  const a = buildDoc("A", 5);
  const b = new SyncNode("B");
  bootstrap(a, b);

  a.deleteAt(2);
  syncOnce(a, b);

  assert.equal(b.text(), a.text());
  assert.equal(b.text().length, 4);
});

test("所有对等端确认后回收增量日志", () => {
  const a = buildDoc("A", 50);
  const b = new SyncNode("B");
  bootstrap(a, b);
  assert.equal(a.doc.logSize, 0, "引导确认后日志已回收");

  a.insertAt(10, "g");
  assert.equal(a.doc.logSize, 1);
  syncOnce(a, b);
  assert.equal(a.doc.logSize, 0, "确认水位覆盖后日志被回收");
});

test("新节点未引导时自动回退全量状态包", () => {
  const a = buildDoc("A", 100);
  const b = new SyncNode("B");
  const packet = a.nextPacketFor("B");
  assert.ok(packet);
  assert.equal(packet.kind, "state");
  const ack = b.receive(packet);
  if (ack) a.receive(ack);
  assert.equal(b.text(), a.text());
  assert.equal(a.nextPacketFor("B"), null, "引导后无变动则静默");
});
