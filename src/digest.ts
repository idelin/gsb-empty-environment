import { crc32 } from "./crc32.ts";

// 增量哈希环 Digest：每个成员贡献一个 4 字节 CRC，环摘要为所有贡献的 XOR。
// 特性：与插入顺序无关、O(1) 增量更新（增/删/改成员无需全量重算）。
export class DigestRing {
  private acc = 0;
  private size = 0;

  add(contribution: number): void {
    this.acc = (this.acc ^ contribution) >>> 0;
    this.size++;
  }

  // XOR 对称：移除即再次异或同一贡献值。
  remove(contribution: number): void {
    this.acc = (this.acc ^ contribution) >>> 0;
    this.size--;
  }

  replace(oldContribution: number, newContribution: number): void {
    this.acc = (this.acc ^ oldContribution ^ newContribution) >>> 0;
  }

  get digest(): number {
    return this.acc;
  }

  get count(): number {
    return this.size;
  }

  get digestHex(): string {
    return this.acc.toString(16).padStart(8, "0");
  }
}

// 成员记录在某条环上的哈希贡献。
export function contributionOf(memberId: string, state: string, incarnation: number): number {
  return crc32(`${memberId}:${state}:${incarnation}`);
}

// 将成员 ID 稳定映射到哈希环区间（用于区间状态对账）。
export function intervalOf(memberId: string, intervalCount: number): number {
  return crc32(memberId) % intervalCount;
}
