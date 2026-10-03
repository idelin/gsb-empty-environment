/**
 * 分片模拟器：产生带独立本地提交时间戳的 CDC 流。
 * 用于演示与测试跨分片转账场景。
 */
import type { CdcEvent } from './types.ts';

export class Shard {
  readonly shardId: string;
  private localTs = 0;

  constructor(shardId: string) {
    this.shardId = shardId;
  }

  /** 本地提交时间戳（分片内单调递增） */
  nextTs(): number {
    return ++this.localTs;
  }

  /** 构造转账事务的“转出（debit）”半边事件 */
  debitEvent(txnId: string, account: string, amount: number, expectedParts: number, participants: string[]): CdcEvent {
    return {
      shardId: this.shardId,
      txnId,
      kind: 'debit',
      account,
      amount,
      commitTs: this.nextTs(),
      closure: { participants, expectedParts },
    };
  }

  /** 构造转账事务的“转入（credit）”半边事件 */
  creditEvent(txnId: string, account: string, amount: number, expectedParts: number, participants: string[]): CdcEvent {
    return {
      shardId: this.shardId,
      txnId,
      kind: 'credit',
      account,
      amount,
      commitTs: this.nextTs(),
      closure: { participants, expectedParts },
    };
  }
}
