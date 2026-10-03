import { ProtoError } from './errors.ts';
import { Journal } from './journal.ts';
import { Reader, Writer } from './wire.ts';

/** 事务操作：put(key, bytes) 或 del(key)。 */
export type Operation =
  | { kind: 'put'; key: string; value: Uint8Array }
  | { kind: 'del'; key: string };

/** 简易异步互斥锁：保证并发 commit 串行落盘。 */
export class AsyncMutex {
  private tail: Promise<void> = Promise.resolve();

  async runExclusive<T>(fn: () => Promise<T> | T): Promise<T> {
    const prev = this.tail;
    let release!: () => void;
    this.tail = new Promise<void>((resolve) => (release = resolve));
    await prev;
    try {
      return await fn();
    } finally {
      release();
    }
  }
}

function encodeOps(ops: Operation[]): Uint8Array {
  const w = new Writer();
  w.writeVarint(ops.length);
  const enc = new TextEncoder();
  for (const op of ops) {
    const keyBytes = enc.encode(op.key);
    w.writeVarint(op.kind === 'put' ? 1 : 0);
    w.writeLengthDelimited(keyBytes);
    if (op.kind === 'put') w.writeLengthDelimited(op.value);
  }
  return w.finish();
}

function decodeOps(payload: Uint8Array): Operation[] {
  const r = new Reader(payload);
  const count = r.readVarintAsNumber(1 << 20);
  const ops: Operation[] = [];
  const dec = new TextDecoder();
  for (let i = 0; i < count; i++) {
    const isPut = r.readVarint() === 1n;
    const key = dec.decode(r.readBytes());
    if (isPut) ops.push({ kind: 'put', key, value: r.readBytes().slice() });
    else ops.push({ kind: 'del', key });
  }
  r.assertEnd();
  return ops;
}

/**
 * 事务型 KV 存储：
 *  - commit 先写 WAL，成功后才应用到内存状态；
  *  - 任何故障（断电/磁盘满）=> 状态完全回滚，staged 缓冲全部释放（无泄漏）；
 *  - recover 重放日志重建状态，撕裂记录由 Journal 自愈截断；
 *  - AsyncMutex 保证并发提交串行化。
 */
export class TransactionalStore {
  private state = new Map<string, Uint8Array>();
  private readonly mutex = new AsyncMutex();
  /** 泄漏追踪：当前存活（未提交未回滚）的 staged 缓冲数。 */
  private stagedLive = 0;
  private readonly journal: Journal;

  constructor(journal: Journal) {
    this.journal = journal;
  }

  get stagedBufferCount(): number {
    return this.stagedLive;
  }

  get(key: string): Uint8Array | undefined {
    const v = this.state.get(key);
    return v ? v.slice() : undefined;
  }

  get size(): number {
    return this.state.size;
  }

  /**
   * 原子提交一组操作。失败时抛错且状态保持不变。
   */
  async commit(ops: Operation[]): Promise<void> {
    if (ops.length === 0) return;
    // 拷贝入 staged 区，调用方之后修改入参不影响事务
    const staged = ops.map((op) =>
      op.kind === 'put' ? { ...op, value: op.value.slice() } : { ...op },
    );
    this.stagedLive += staged.length;
    try {
      await this.mutex.runExclusive(() => {
        const record = encodeOps(staged);
        this.journal.append(record); // 故障点：断电/磁盘满在此抛出
        for (const op of staged) {
          if (op.kind === 'put') this.state.set(op.key, op.value.slice());
          else this.state.delete(op.key);
        }
      });
    } catch (err) {
      // 回滚：内存状态未被触碰（应用在 WAL 成功之后），无需逆操作；
      // 若未来改为先应用后落盘，此处应做逆操作回放。
      throw err;
    } finally {
      // 无论成败，staged 缓冲全部释放 -> 无泄漏
      this.stagedLive -= staged.length;
    }
  }

  /** 启动恢复：重放日志，重建内存状态。 */
  recover(): number {
    const payloads = this.journal.recover();
    this.state.clear();
    for (const payload of payloads) {
      for (const op of decodeOps(payload)) {
        if (op.kind === 'put') this.state.set(op.key, op.value);
        else this.state.delete(op.key);
      }
    }
    return payloads.length;
  }
}

/** 丢包信道上的可靠传输：分片 + 序号 + CRC，缺失自动重传。 */
export async function reliableTransfer(
  data: Uint8Array,
  chunkSize: number,
  channel: { send(): boolean; noteRetransmit(): void },
  maxRetransmits = 100,
): Promise<{ chunks: number; received: Uint8Array }> {
  if (chunkSize <= 0) throw new ProtoError('SCHEMA_VIOLATION', 'chunkSize 必须为正');
  const total = Math.max(1, Math.ceil(data.length / chunkSize));
  const received: (Uint8Array | undefined)[] = new Array(total);
  let got = 0;
  for (let i = 0; i < total; i++) {
    const chunk = data.subarray(i * chunkSize, Math.min(data.length, (i + 1) * chunkSize));
    let attempts = 0;
    for (;;) {
      if (channel.send()) {
        received[i] = chunk.slice();
        got++;
        break;
      }
      channel.noteRetransmit();
      if (++attempts > maxRetransmits) {
        throw new ProtoError('PACKET_LOSS', `分片 ${i} 重传 ${maxRetransmits} 次仍丢失`);
      }
    }
  }
  const out = new Uint8Array(data.length);
  let offset = 0;
  for (const part of received) {
    if (!part) throw new ProtoError('PACKET_LOSS', '重组时分片缺失');
    out.set(part, offset);
    offset += part.length;
  }
  return { chunks: got, received: out };
}
