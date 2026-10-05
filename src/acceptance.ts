import { manifestDigest, sha256Hex } from './digest';
import { ContentStore } from './store';
import type {
  AcceptanceState,
  ArtifactManifest,
  ChunkRef,
  ExpectedContext,
  Receipt,
  RejectReason,
} from './types';

export interface AcceptedResult {
  taskId: string;
  attempt: number;
  toolVersion: string;
  inputDigest: string;
  files: Array<{ path: string; sha256: string }>;
}

/**
 * 远程执行验收状态机。
 *
 * 不变量：
 * 1. 只接受 inputDigest / toolVersion / attempt 与当前期待上下文完全一致的清单；
 * 2. 重复清单幂等，不打断已在进行的补取；
 * 3. acceptedAttempt 单调递增——旧代次的任何后续事件都不得覆盖已接受的新代次；
 * 4. 校验失败即清理该代次的全部已收内容。
 */
export class AcceptanceController {
  private expected: ExpectedContext;
  private readonly store: ContentStore;

  private state: AcceptanceState = 'awaiting-manifest';
  private manifest?: ArtifactManifest;
  private manifestHash?: string;

  private acceptedAttempt = -1;
  private acceptedResult?: AcceptedResult;
  private failReason?: string;

  constructor(expected: ExpectedContext, store: ContentStore = new ContentStore()) {
    this.expected = expected;
    this.store = store;
  }

  getState(): AcceptanceState {
    return this.state;
  }

  getAcceptedResult(): AcceptedResult | undefined {
    return this.acceptedResult;
  }

  getFailReason(): string | undefined {
    return this.failReason;
  }

  /** 当前仍缺失、需要向执行器补取的分块。 */
  missingChunks(): ChunkRef[] {
    if (this.manifest === undefined) return [];
    const missing: ChunkRef[] = [];
    for (const file of this.manifest.files) {
      for (const chunk of file.chunks) {
        if (!this.store.has(this.expected.attempt, file.path, chunk.index)) {
          missing.push({ path: file.path, index: chunk.index });
        }
      }
    }
    return missing;
  }

  /** 上报产物清单。 */
  submitManifest(manifest: ArtifactManifest): Receipt {
    // 不变量 3：旧代次不得覆盖已接受的新代次。
    if (manifest.attempt <= this.acceptedAttempt) {
      return this.reject('stale-attempt');
    }
    // 不变量 1：输入摘要、工具版本、代次必须逐项匹配。
    if (
      manifest.taskId !== this.expected.taskId ||
      manifest.inputDigest !== this.expected.inputDigest ||
      manifest.toolVersion !== this.expected.toolVersion ||
      manifest.attempt !== this.expected.attempt
    ) {
      return this.reject('context-mismatch');
    }
    if (this.state === 'failed') {
      return this.reject('unexpected-state');
    }

    // 不变量 2：重复清单幂等。
    const hash = manifestDigest(manifest);
    if (this.manifestHash === hash) {
      return { ok: true, reason: 'duplicate-manifest', state: this.state };
    }
    if (this.manifestHash !== undefined) {
      // 同代次但内容不同的清单：视为上下文不匹配，拒绝替换。
      return this.reject('context-mismatch');
    }

    this.manifest = manifest;
    this.manifestHash = hash;
    this.state = 'fetching';
    this.advanceIfComplete();
    return { ok: true, state: this.state };
  }

  /** 执行器推送一个内容分块（含补取响应）。 */
  supplyChunk(path: string, index: number, data: Buffer): Receipt {
    if (this.state !== 'fetching' || this.manifest === undefined) {
      return this.reject('unexpected-state');
    }
    const file = this.manifest.files.find((f) => f.path === path);
    const spec = file?.chunks.find((c) => c.index === index);
    if (file === undefined || spec === undefined) {
      return this.reject('unknown-chunk');
    }
    this.store.put(this.expected.attempt, path, index, data);
    this.advanceIfComplete();
    return { ok: true, state: this.state };
  }

  /** 远程执行器的“成功”回报：旧代次的迟到成功不得覆盖已接受的新代次。 */
  notifyRemoteSuccess(attempt: number): boolean {
    if (attempt <= this.acceptedAttempt) return false;
    if (attempt !== this.expected.attempt) return false;
    return this.state === 'accepted';
  }

  /** 升级代次，为下一次尝试重新武装状态机。 */
  expectAttempt(next: ExpectedContext): void {
    if (next.attempt <= this.acceptedAttempt) {
      throw new Error(
        `attempt ${next.attempt} would regress below accepted attempt ${this.acceptedAttempt}`,
      );
    }
    this.expected = next;
    this.manifest = undefined;
    this.manifestHash = undefined;
    this.failReason = undefined;
    this.state = 'awaiting-manifest';
  }

  private advanceIfComplete(): void {
    if (this.state !== 'fetching') return;
    if (this.missingChunks().length > 0) return;
    this.verify();
  }

  private verify(): void {
    const manifest = this.manifest;
    if (manifest === undefined) return;
    this.state = 'verifying';
    for (const file of manifest.files) {
      for (const chunk of file.chunks) {
        const data = this.store.get(this.expected.attempt, file.path, chunk.index);
        if (data === undefined || data.length !== chunk.size || sha256Hex(data) !== chunk.sha256) {
          this.fail(`chunk checksum mismatch: ${file.path}#${chunk.index}`);
          return;
        }
      }
      const assembled = this.store.assemble(this.expected.attempt, file);
      if (assembled.length !== file.size || sha256Hex(assembled) !== file.sha256) {
        this.fail(`file checksum mismatch: ${file.path}`);
        return;
      }
    }
    this.state = 'accepted';
    this.acceptedAttempt = this.expected.attempt;
    this.acceptedResult = {
      taskId: manifest.taskId,
      attempt: manifest.attempt,
      toolVersion: manifest.toolVersion,
      inputDigest: manifest.inputDigest,
      files: manifest.files.map((f) => ({ path: f.path, sha256: f.sha256 })),
    };
  }

  private fail(reason: string): void {
    this.failReason = reason;
    // 不变量 4：校验失败，清理该代次全部已收内容。
    this.store.purgeAttempt(this.expected.attempt);
    this.state = 'failed';
  }

  private reject(reason: RejectReason): Receipt {
    return { ok: false, reason, state: this.state };
  }
}
