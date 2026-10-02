import { DocumentCRDT, type ElementState, type Op } from "./document-crdt.ts";
import { minVersionVectors, type VersionVector } from "./version-vector.ts";

/**
 * 线协议数据包：
 * - state：全量状态快照（初始引导 / 对照组）。
 * - delta：自 base 水位以来的增量操作切片。
 * - ack：  接收端回传的确认水位（其当前版本向量）。
 */
export type Packet =
  | { kind: "state"; from: string; to: string; vv: VersionVector; elements: ElementState[] }
  | { kind: "delta"; from: string; to: string; base: VersionVector; ops: Op[] }
  | { kind: "ack"; from: string; to: string; vv: VersionVector };

export function packetBytes(packet: Packet): number {
  return Buffer.byteLength(JSON.stringify(packet), "utf8");
}

/**
 * 同步节点：为每个对等端维护一条“远端已确认水位”，
 * 只生成并发送自上次确认之后发生变动的 Delta 切片；
 * 无变动时不产生任何流量。
 */
export class SyncNode {
  readonly id: string;
  readonly doc: DocumentCRDT;
  private watermarks = new Map<string, VersionVector>();

  constructor(id: string) {
    this.id = id;
    this.doc = new DocumentCRDT(id);
  }

  insertAt(index: number, char: string): string {
    return this.doc.insertAt(index, char);
  }

  deleteAt(index: number): void {
    this.doc.deleteAt(index);
  }

  text(): string {
    return this.doc.text();
  }

  /** 远端对等端当前已确认的水位。 */
  watermarkFor(peerId: string): VersionVector {
    return { ...(this.watermarks.get(peerId) ?? {}) };
  }

  /** 全量状态包（用于新节点引导）。 */
  statePacketFor(peerId: string): Packet {
    const snap = this.doc.snapshot();
    return { kind: "state", from: this.id, to: peerId, vv: snap.vv, elements: snap.elements };
  }

  /**
   * 为指定对等端生成增量 Delta 包；
   * 若自上次确认水位以来无任何变动，返回 null（不发送）。
   */
  deltaPacketFor(peerId: string): Packet | null {
    const base = this.watermarks.get(peerId) ?? {};
    const ops = this.doc.deltaSince(base);
    if (ops.length === 0) return null;
    return { kind: "delta", from: this.id, to: peerId, base: { ...base }, ops };
  }

  /** 发往对等端的下一个包：未引导过给全量，否则给增量（无变动为 null）。 */
  nextPacketFor(peerId: string): Packet | null {
    if (!this.watermarks.has(peerId)) return this.statePacketFor(peerId);
    return this.deltaPacketFor(peerId);
  }

  /** 处理入站包；需要回执时返回 ack 包，否则返回 null。 */
  receive(packet: Packet): Packet | null {
    if (packet.to !== this.id) return null;
    switch (packet.kind) {
      case "state":
        this.doc.mergeState({ vv: packet.vv, elements: packet.elements });
        return this.ackFor(packet.from);
      case "delta":
        this.doc.merge(packet.ops);
        return this.ackFor(packet.from);
      case "ack":
        this.watermarks.set(packet.from, packet.vv);
        this.collectGarbage();
        return null;
    }
  }

  private ackFor(peerId: string): Packet {
    return { kind: "ack", from: this.id, to: peerId, vv: this.doc.version };
  }

  /** 所有已知对等端都确认过的日志前缀可以安全回收。 */
  private collectGarbage(): void {
    if (this.watermarks.size === 0) return;
    let coverage: VersionVector | null = null;
    for (const wm of this.watermarks.values()) {
      coverage = coverage === null ? { ...wm } : minVersionVectors(coverage, wm);
    }
    if (coverage) this.doc.gc(coverage);
  }
}
