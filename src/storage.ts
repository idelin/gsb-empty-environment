import { StaleFenceError } from './errors.ts';

/**
 * 带 Fencing 的存储：记录已见的最大 epoch，
 * 拒绝一切携带更小 epoch 的写入——即使旧主“僵尸复活”也无法落盘。
 */
export class FencedStorage {
  readonly dcId: string;
  private readonly data = new Map<string, unknown>();
  private highestEpoch = 0;

  constructor(dcId: string) {
    this.dcId = dcId;
  }

  write(key: string, value: unknown, epoch: number): void {
    if (epoch < this.highestEpoch) {
      throw new StaleFenceError(epoch, this.highestEpoch);
    }
    this.highestEpoch = epoch;
    this.data.set(key, value);
  }

  read(key: string): unknown {
    return this.data.get(key);
  }

  fencedEpoch(): number {
    return this.highestEpoch;
  }
}
