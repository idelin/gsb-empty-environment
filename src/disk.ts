import type { FaultInjector, BoundaryPoint } from './faults.ts';

/**
 * 模拟耐用存储（磁盘）。系统“崩溃重启”后其中的数据仍然保留；
 * 进程的易失内存状态则全部丢失，需通过重放日志恢复。
 *
 * append 把“日志落盘 + 内存应用”建模为一个原子单元：
 * 崩溃只能发生在该单元之前（写入丢失）或之后（写入已持久）。
 */
export class SimulatedDisk<Record> {
  private records: Record[] = [];
  private readonly faults: FaultInjector;

  constructor(faults: FaultInjector) {
    this.faults = faults;
  }

  append(record: Record, boundary: BoundaryPoint, apply: () => void = () => {}): void {
    this.faults.check(boundary, 'before');
    this.records.push(record);
    apply();
    this.faults.check(boundary, 'after');
  }

  readAll(): Record[] {
    return this.records.slice();
  }
}
