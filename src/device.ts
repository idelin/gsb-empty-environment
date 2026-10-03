/**
 * 模拟块设备 + 故障注入封装。
 * FaultyBlockDevice 可注入: 突发断电(撕裂写, 只落盘部分字节)、磁盘满(ENOSPC)。
 */

import { InjectedFaultError } from './errors.ts';
import { FaultInjector } from './faults.ts';

export class BlockDevice {
  protected data: Uint8Array;
  readonly capacity: number;

  constructor(capacity: number) {
    this.capacity = capacity;
    this.data = new Uint8Array(capacity);
  }

  get size(): number {
    return this.capacity;
  }

  write(offset: number, bytes: Uint8Array): void {
    if (offset + bytes.length > this.capacity) {
      throw new InjectedFaultError('disk-full', '设备容量不足');
    }
    this.data.set(bytes, offset);
  }

  read(offset: number, length: number): Uint8Array {
    return this.data.slice(offset, offset + length);
  }

  /** fsync 语义 (内存设备为 no-op)。 */
  sync(): void {}
}

export interface PowerLossEvent {
  offset: number;
  requestedBytes: number;
  persistedBytes: number;
}

/** 带故障注入的块设备。 */
export class FaultyBlockDevice extends BlockDevice {
  readonly injector: FaultInjector;
  readonly powerLossEvents: PowerLossEvent[] = [];
  /** 断电时最少保留的字节比例 (模拟撕裂写)。 */
  tornWriteRatio: number;

  constructor(capacity: number, injector: FaultInjector, tornWriteRatio = 0.5) {
    super(capacity);
    this.injector = injector;
    this.tornWriteRatio = tornWriteRatio;
  }

  override write(offset: number, bytes: Uint8Array): void {
    if (this.injector.shouldFire('disk-full')) {
      throw new InjectedFaultError('disk-full', `写入 ${bytes.length} 字节时磁盘满`);
    }
    if (this.injector.shouldFire('power-loss')) {
      // 撕裂写: 只有前缀部分真正落盘, 随后"断电"
      const persisted = Math.max(0, Math.floor(bytes.length * this.tornWriteRatio));
      if (persisted > 0) {
        super.write(offset, bytes.subarray(0, persisted));
      }
      this.powerLossEvents.push({
        offset,
        requestedBytes: bytes.length,
        persistedBytes: persisted,
      });
      throw new InjectedFaultError(
        'power-loss',
        `突发断电: ${bytes.length} 字节仅落盘 ${persisted} 字节`,
      );
    }
    super.write(offset, bytes);
  }

  /** 模拟重启: 返回挂载同一盘片的新设备视图。 */
  reboot(): FaultyBlockDevice {
    const dev = new FaultyBlockDevice(this.capacity, this.injector, this.tornWriteRatio);
    dev.data.set(this.data);
    dev.powerLossEvents.push(...this.powerLossEvents);
    return dev;
  }
}
