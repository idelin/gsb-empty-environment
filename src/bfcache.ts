/**
 * BFCache 冻结与恢复。
 *
 * 页面进入 BFCache 时，其浏览上下文与全部在途请求输入被深冻结；
 * 恢复时原样还原。暂定导航（tentative navigation）与策略升版都不得
 * 修改冻结快照，因此在途请求始终按发起时的策略版本与上下文重算。
 */

import type { FrameNode, RequestInput } from './types.ts';

export interface FrozenPage {
  readonly pageId: string;
  readonly frozenAtMs: number;
  readonly policyVersionAtFreeze: string;
  readonly contextTree: FrameNode;
  readonly inFlightRequests: readonly RequestInput[];
  /** 冻结序号，恢复时校验页面身份未被替换。 */
  readonly freezeSeq: number;
}

export interface RestoredPage {
  readonly pageId: string;
  readonly restoredAtMs: number;
  readonly contextTree: FrameNode;
  readonly inFlightRequests: readonly RequestInput[];
  readonly policyVersionAtFreeze: string;
  readonly freezeSeq: number;
}

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === 'object' && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const key of Object.keys(value as Record<string, unknown>)) {
      deepFreeze((value as Record<string, unknown>)[key]);
    }
  }
  return value;
}

export class BFCacheStore {
  private readonly pages = new Map<string, FrozenPage>();
  private seq = 0;

  freeze(
    pageId: string,
    contextTree: FrameNode,
    inFlightRequests: readonly RequestInput[],
    frozenAtMs: number,
  ): FrozenPage {
    const snapshot: FrozenPage = deepFreeze({
      pageId,
      frozenAtMs,
      policyVersionAtFreeze: inFlightRequests[0]?.policyVersion ?? '',
      contextTree: structuredClone(contextTree),
      inFlightRequests: structuredClone(inFlightRequests),
      freezeSeq: (this.seq += 1),
    });
    this.pages.set(pageId, snapshot);
    return snapshot;
  }

  restore(pageId: string, restoredAtMs: number): RestoredPage {
    const frozen = this.pages.get(pageId);
    if (!frozen) {
      throw new Error(`no frozen page in BFCache: ${pageId}`);
    }
    // 原样还原冻结上下文；策略版本以冻结时刻为准，忽略当前升版。
    return {
      pageId: frozen.pageId,
      restoredAtMs,
      contextTree: frozen.contextTree,
      inFlightRequests: frozen.inFlightRequests,
      policyVersionAtFreeze: frozen.policyVersionAtFreeze,
      freezeSeq: frozen.freezeSeq,
    };
  }

  has(pageId: string): boolean {
    return this.pages.has(pageId);
  }

  evict(pageId: string): boolean {
    return this.pages.delete(pageId);
  }
}
