import { ProtoError } from './errors.ts';

/** 可注入故障的字节接收端抽象（模拟磁盘/网络设备）。 */
export interface ByteSink {
  /** 全量读取当前内容。 */
  read(): Uint8Array;
  /** 追加写入；故障注入时可能只落盘部分字节后抛错。 */
  append(data: Uint8Array): void;
  /** 截断到指定长度（自愈用）。 */
  truncate(size: number): void;
  get size(): number;
}

/** 正常工作的内存设备。 */
export class MemorySink implements ByteSink {
  private data: Uint8Array = new Uint8Array(0);

  read(): Uint8Array {
    return this.data.slice();
  }

  append(chunk: Uint8Array): void {
    const next = new Uint8Array(this.data.length + chunk.length);
    next.set(this.data, 0);
    next.set(chunk, this.data.length);
    this.data = next;
  }

  truncate(size: number): void {
    if (size < this.data.length) this.data = this.data.slice(0, size);
  }

  get size(): number {
    return this.data.length;
  }
}

export interface FaultPlan {
  /** 写入进行到第 N 字节时"断电"：部分字节落盘后抛 POWER_LOSS。 */
  powerLossAfterBytes?: number;
  /** 追加写入一律抛 DISK_FULL，且不落任何字节。 */
  diskFull?: boolean;
  /** 在第 N 次（1 起）append 时抛 DISK_FULL，用于并发场景的确定性注入。 */
  diskFullOnAppends?: number[];
}

/**
 * 故障注入包装器：拦截 append，按脚本注入断电/磁盘满。
 * 统计注入次数，供测试断言"故障确实被触发"。
 */
export class FaultySink implements ByteSink {
  plan: FaultPlan = {};
  readonly stats = { powerLoss: 0, diskFull: 0, appends: 0 };
  private readonly inner: ByteSink;

  constructor(inner: ByteSink) {
    this.inner = inner;
  }

  read(): Uint8Array {
    return this.inner.read();
  }

  append(data: Uint8Array): void {
    this.stats.appends++;
    if (this.plan.diskFull || this.plan.diskFullOnAppends?.includes(this.stats.appends)) {
      this.stats.diskFull++;
      throw new ProtoError('DISK_FULL', '注入故障：磁盘已满，写入被拒绝');
    }
    if (this.plan.powerLossAfterBytes !== undefined) {
      const n = Math.min(this.plan.powerLossAfterBytes, data.length);
      this.stats.powerLoss++;
      this.plan.powerLossAfterBytes = undefined; // 断电只发生一次
      if (n > 0) this.inner.append(data.subarray(0, n)); // 撕裂写：部分落盘
      throw new ProtoError('POWER_LOSS', `注入故障：写入 ${n}/${data.length} 字节后断电`);
    }
    this.inner.append(data);
  }

  truncate(size: number): void {
    this.inner.truncate(size);
  }

  get size(): number {
    return this.inner.size;
  }
}

/**
 * 丢包网络模拟：把数据分片传输，按确定性序列丢弃分片。
 * 接收端用序号+校验和重组，缺失分片触发重传，直到完整。
 */
export class LossyChannel {
  private nextDropIndex = 0;
  readonly stats = { sent: 0, dropped: 0, retransmitted: 0 };

  /**
   * @param dropAtSendNumbers 第几次"发送"时丢包（含重传），严格递增、无重复。
   */
  private readonly dropAtSendNumbers: number[];

  constructor(dropAtSendNumbers: number[] = []) {
    this.dropAtSendNumbers = dropAtSendNumbers;
  }

  /** 模拟一次发送：返回 true 表示对端收到。 */
  send(): boolean {
    this.stats.sent++;
    if (this.nextDropIndex < this.dropAtSendNumbers.length &&
        this.dropAtSendNumbers[this.nextDropIndex] === this.stats.sent) {
      this.nextDropIndex++;
      this.stats.dropped++;
      return false;
    }
    return true;
  }

  noteRetransmit(): void {
    this.stats.retransmitted++;
  }
}
