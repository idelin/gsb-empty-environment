import { Entry, Key, Op, WriteResult } from './types';

export interface SourceOptions {
  /** 模拟单次请求的处理延迟（毫秒） */
  latencyMs: () => number;
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/**
 * 甲：迁出方分片。
 * - 栅栏前：正常接受写，分配全局递增 seq，应用本地并追加 oplog。
 * - 栅栏后：拒绝新写；栅栏前已接受（在途）的请求继续完成并返回结果。
 */
export class SourceNode {
  private data = new Map<Key, Entry>();
  private oplog: Op[] = [];
  private seq = 0;
  private fenced = false;
  private inflight = 0;

  constructor(private readonly opts: SourceOptions) {}

  get isFenced(): boolean {
    return this.fenced;
  }

  get inflightCount(): number {
    return this.inflight;
  }

  get highWaterSeq(): number {
    return this.seq;
  }

  /** 落下切换栅栏：从此刻起拒绝新写 */
  fence(): void {
    this.fenced = true;
  }

  async write(key: Key, value: string | null): Promise<WriteResult> {
    if (this.fenced) {
      return { ok: false, reason: 'fenced' };
    }
    const op: Op = { seq: ++this.seq, key, kind: value === null ? 'del' : 'put', value };
    this.applyLocal(op);
    this.oplog.push(op);
    this.inflight++;
    await sleep(this.opts.latencyMs());
    this.inflight--;
    // 即使等待期间栅栏已落下，旧请求依然正常返回结果
    return { ok: true, op };
  }

  private applyLocal(op: Op): void {
    if (op.kind === 'del') {
      this.data.delete(op.key);
    } else {
      this.data.set(op.key, { seq: op.seq, value: op.value });
    }
  }

  /** 基础数据复制：当前可见 key 列表（复制期间仍在变化，允许不一致） */
  listKeys(): Key[] {
    return [...this.data.keys()];
  }

  /** 基础数据复制：单条时点读取，携带版本号 */
  readEntry(key: Key): { key: Key; entry: Entry } | undefined {
    const entry = this.data.get(key);
    return entry ? { key, entry: { ...entry } } : undefined;
  }

  /** 增量复制：读取 seq（不含）之后的全部变更 */
  oplogAfter(seq: number): Op[] {
    return this.oplog.filter((o) => o.seq > seq);
  }

  opBySeq(seq: number): Op | undefined {
    return this.oplog.find((o) => o.seq === seq);
  }
}
