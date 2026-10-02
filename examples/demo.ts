import { SyncNode, packetBytes, type Packet } from "../src/sync-node.ts";

const SIZE = 10_000;

function bootstrap(from: SyncNode, to: SyncNode): number {
  const state = from.statePacketFor(to.id);
  const bytes = packetBytes(state);
  const ack = to.receive(state);
  if (ack) from.receive(ack);
  return bytes;
}

function deliver(packet: Packet | null, to: SyncNode, backTo: SyncNode): void {
  if (!packet) return;
  const ack = to.receive(packet);
  if (ack) backTo.receive(ack);
}

console.log("=== Delta-CRDT 增量压缩同步演示 ===\n");
console.log(`构建 ${SIZE} 字符文档（每字符一个 CRDT 节点）...`);
const a = new SyncNode("A");
let tail: string | null = null;
for (let i = 0; i < SIZE; i++) tail = a.doc.insertAfter(tail, "x");

const b = new SyncNode("B");
const c = new SyncNode("C");

const fullBytes = bootstrap(a, b);
bootstrap(a, c);
console.log(`[引导] 全量状态包大小: ${fullBytes.toLocaleString()} 字节`);

// 关键场景：10000 节点文档中仅修改 1 个字符
a.insertAt(SIZE / 2, "Z");

const delta = a.deltaPacketFor("B")!;
const deltaBytes = packetBytes(delta);
console.log(`\n[修改] 在位置 ${SIZE / 2} 插入 1 个字符 'Z'`);
console.log(`[增量] Delta 包大小: ${deltaBytes} 字节（常数级，与文档规模无关）`);
console.log(`[压缩] ${(fullBytes / deltaBytes).toFixed(0)}x（全量 ${fullBytes.toLocaleString()}B -> 增量 ${deltaBytes}B）`);
console.log(`[内容] ${JSON.stringify(delta)}`);

deliver(delta, b, a);
console.log(`\n[校验] B 应用 Delta 后与 A 完全等价: ${b.text() === a.text()}`);
console.log(`[水位] A 记录的 B 端确认水位: ${JSON.stringify(a.watermarkFor("B"))}`);
console.log(`[去重] 确认后再次生成 Delta: ${a.deltaPacketFor("B") === null ? "null（无变动不发送）" : "非空（异常！)"}`);

// 对照：C 走传统全量 State-based 传输达到同一状态
deliver(a.statePacketFor("C"), c, a);
console.log(`[对照] C 经全量传输后与 A 完全等价: ${c.text() === a.text()}`);

// 连续多次小编辑，Delta 仍然保持常数级
a.deleteAt(0);
a.insertAt(9_999, "!");
const delta2 = a.deltaPacketFor("B")!;
console.log(`\n[连续] 再做 1 次删除 + 1 次插入，Delta 包: ${packetBytes(delta2)} 字节（含 ${(delta2 as { ops: unknown[] }).ops.length} 个操作）`);
deliver(delta2, b, a);
console.log(`[校验] B 再次同步后等价: ${b.text() === a.text()}`);
