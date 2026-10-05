import type { PartitionId } from './store.ts';

/**
 * 会话：记录客户端在各分区上已见到的最高版本水位。
 * 后续读取不得返回低于水位的版本（单调读 / 读己之写）。
 * 会话可序列化为 token 跨请求传递，合并时按分区取最大水位。
 */
export class Session {
  private readonly watermarks = new Map<PartitionId, number>();

  /** 某分区当前水位；从未见过则为 0。 */
  watermark(partition: PartitionId): number {
    return this.watermarks.get(partition) ?? 0;
  }

  /** 记录响应中的版本水位，只升不降。 */
  record(partition: PartitionId, version: number): void {
    if (version > this.watermark(partition)) {
      this.watermarks.set(partition, version);
    }
  }

  /** 各分区水位快照（用于断言与调试）。 */
  snapshot(): ReadonlyMap<PartitionId, number> {
    return new Map(this.watermarks);
  }

  /** 合并两个会话：每个分区取最大水位，返回新会话。 */
  static merge(a: Session, b: Session): Session {
    const merged = new Session();
    for (const src of [a, b]) {
      for (const [partition, version] of src.watermarks) {
        merged.record(partition, version);
      }
    }
    return merged;
  }

  static mergeAll(...sessions: Session[]): Session {
    return sessions.reduce((acc, s) => Session.merge(acc, s), new Session());
  }

  /** 序列化为可随请求传递的 token。 */
  toToken(): string {
    const obj: Record<string, number> = {};
    for (const [partition, version] of this.watermarks) obj[partition] = version;
    return Buffer.from(JSON.stringify(obj), 'utf8').toString('base64url');
  }

  static fromToken(token: string): Session {
    const obj = JSON.parse(Buffer.from(token, 'base64url').toString('utf8')) as Record<
      string,
      number
    >;
    const session = new Session();
    for (const [partition, version] of Object.entries(obj)) {
      session.record(partition, version);
    }
    return session;
  }
}
