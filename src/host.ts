import { bytesToHex, hexToBytes, sha256 } from "./bytes.ts";
import type { Chain, DeployCallback } from "./chain.ts";
import type { Authority, DeploymentRecord, Receipt } from "./record.ts";
import {
  recordAddress,
  recordDigest,
  validateRecord,
} from "./record.ts";

export class CallError extends Error {}

interface ActiveGrant {
  addressHex: string;
  generation: number;
  runtimeDigestHex: string;
  txId: number;
  blockHeight: number;
}

/**
 * 安全宿主：预授权只针对完整部署记录签发；部署回调中地址与
 * runtimeCode 摘要全部匹配才激活；外部 Call 前后都核对当前
 * 代码摘要与代次，执行中的代码变化使调用失败。
 */
export class SecureHost {
  private readonly records = new Map<string, DeploymentRecord>();
  private readonly receipts = new Map<string, Receipt>();
  private readonly active = new Map<string, ActiveGrant>();
  /** 被 Destroy 波及过的记录摘要：旧授权永久失效。 */
  private readonly invalidated = new Set<string>();
  readonly sensitiveLog: string[] = [];
  readonly chain: Chain;
  readonly authority: Authority;

  constructor(chain: Chain, authority: Authority) {
    this.chain = chain;
    this.authority = authority;
  }

  /** 登记完整部署记录；记录必须指向地址当前代次。 */
  submitRecord(record: DeploymentRecord): string {
    validateRecord(record);
    if (record.chainId !== this.chain.chainId) {
      throw new Error("record chainId does not match this chain");
    }
    const current = this.chain.generationOf(recordAddress(record));
    if (record.generation !== current) {
      throw new Error(
        `record generation ${record.generation} != current generation ${current}`,
      );
    }
    const key = bytesToHex(recordDigest(record));
    if (this.invalidated.has(key)) {
      throw new Error("record belongs to a permanently invalidated grant");
    }
    this.records.set(key, record);
    return key;
  }

  /** 预授权只能针对完整记录签发，不存在“只按地址”的入口。 */
  issuePreAuthorization(recordDigestHex: string): Receipt {
    const record = this.records.get(recordDigestHex);
    if (!record) {
      throw new Error("pre-authorization requires a complete deployment record");
    }
    if (this.invalidated.has(recordDigestHex)) {
      throw new Error("record permanently invalidated");
    }
    const receipt = this.authority.issue(record);
    this.receipts.set(recordDigestHex, receipt);
    return receipt;
  }

  /** 部署回调：地址与 runtime 摘要全匹配才激活权限。 */
  onDeployed(callback: DeployCallback, receipt: Receipt): boolean {
    const ctx = this.tryLoadAuthorizedRecord(receipt);
    if (!ctx) return false;
    const { key, record } = ctx;
    if (!callback.success || !callback.runtimeCode) return false;
    if (callback.runtimeCode.length === 0) return false;
    const expectedAddress = recordAddress(record);
    if (bytesToHex(callback.address) !== bytesToHex(expectedAddress)) {
      return false;
    }
    if (callback.generation !== record.generation) return false;
    if (
      bytesToHex(sha256(callback.runtimeCode)) !==
      bytesToHex(record.expectedRuntimeCodeDigest)
    ) {
      return false;
    }
    if (this.chain.generationOf(expectedAddress) !== record.generation) {
      return false;
    }
    const currentDigest = this.chain.codeDigestOf(expectedAddress);
    if (
      !currentDigest ||
      bytesToHex(currentDigest) !==
        bytesToHex(record.expectedRuntimeCodeDigest)
    ) {
      return false;
    }
    this.active.set(key, {
      addressHex: bytesToHex(expectedAddress),
      generation: record.generation,
      runtimeDigestHex: bytesToHex(record.expectedRuntimeCodeDigest),
      txId: callback.txId,
      blockHeight: callback.blockHeight,
    });
    return true;
  }

  /** Destroy 后：该地址旧代次的授权永久失效。 */
  onDestroyed(address: Uint8Array): void {
    const addressHex = bytesToHex(address);
    const currentGeneration = this.chain.generationOf(address);
    for (const [key, grant] of [...this.active]) {
      if (grant.addressHex === addressHex) {
        this.active.delete(key);
        this.invalidated.add(key);
      }
    }
    for (const [key, record] of this.records) {
      if (
        bytesToHex(recordAddress(record)) === addressHex &&
        record.generation < currentGeneration
      ) {
        this.invalidated.add(key);
      }
    }
  }

  /**
   * 外部 Call：每次执行前核对当前代码摘要与代次；
   * `during` 模拟被调代码的执行（可能重入 Destroy/Deploy），
   * 执行后再次核对，代码变化使调用失败。
   */
  call(receipt: Receipt, during?: () => void): string {
    const { key } = this.loadAuthorizedRecord(receipt);
    const grant = this.active.get(key);
    if (!grant) throw new CallError("no active grant for this record");
    const address = hexToBytes(grant.addressHex);
    const beforeDigest = this.chain.codeDigestOf(address);
    const beforeGeneration = this.chain.generationOf(address);
    if (beforeGeneration !== grant.generation) {
      throw new CallError("generation changed since activation");
    }
    if (!beforeDigest || bytesToHex(beforeDigest) !== grant.runtimeDigestHex) {
      throw new CallError("runtime code digest mismatch before call");
    }
    during?.();
    const afterDigest = this.chain.codeDigestOf(address);
    const afterGeneration = this.chain.generationOf(address);
    if (
      afterGeneration !== beforeGeneration ||
      !afterDigest ||
      bytesToHex(afterDigest) !== bytesToHex(beforeDigest)
    ) {
      throw new CallError("code changed during execution");
    }
    const message = `sensitive-callback@${grant.addressHex}#g${grant.generation}`;
    this.sensitiveLog.push(message);
    return message;
  }

  isActive(recordDigestHex: string): boolean {
    return this.active.has(recordDigestHex);
  }

  private tryLoadAuthorizedRecord(
    receipt: Receipt,
  ): { key: string; record: DeploymentRecord } | null {
    try {
      return this.loadAuthorizedRecord(receipt);
    } catch {
      return null;
    }
  }

  private loadAuthorizedRecord(receipt: Receipt): {
    key: string;
    record: DeploymentRecord;
  } {
    if (!this.authority.verify(receipt)) {
      throw new CallError("receipt tag invalid");
    }
    if (receipt.chainId !== this.chain.chainId) {
      throw new CallError("receipt chainId does not match this chain");
    }
    const key = bytesToHex(receipt.recordDigest);
    const record = this.records.get(key);
    if (!record) throw new CallError("unknown deployment record");
    if (this.invalidated.has(key)) {
      throw new CallError("authorization permanently invalidated");
    }
    return { key, record };
  }
}

/**
 * 待审实现（存在缺陷，仅用于对照与攻击搜索）：
 * 授权仅按地址缓存——激活时不记录代次，Call 前也不重新核对
 * 当前代码摘要，因此 Destroy 后同地址重建的不同 runtimeCode
 * 仍能命中旧缓存并执行敏感回调。
 */
export class VulnerableHost {
  private readonly pending = new Map<string, Uint8Array>();
  private readonly activeByAddress = new Map<string, Uint8Array>();
  readonly sensitiveLog: string[] = [];
  readonly chain: Chain;

  constructor(chain: Chain) {
    this.chain = chain;
  }

  preAuthorize(address: Uint8Array, expectedRuntimeDigest: Uint8Array): void {
    this.pending.set(bytesToHex(address), expectedRuntimeDigest);
  }

  onDeployed(callback: DeployCallback): boolean {
    const addressHex = bytesToHex(callback.address);
    const expected = this.pending.get(addressHex);
    if (!expected || !callback.success || !callback.runtimeCode) return false;
    if (bytesToHex(sha256(callback.runtimeCode)) !== bytesToHex(expected)) {
      return false;
    }
    // 缺陷：只按地址缓存授权，丢失代次与后续代码核对。
    this.activeByAddress.set(addressHex, expected);
    this.pending.delete(addressHex);
    return true;
  }

  call(address: Uint8Array): string {
    const addressHex = bytesToHex(address);
    if (!this.activeByAddress.has(addressHex)) {
      throw new CallError("not authorized");
    }
    if (this.chain.codeOf(address) === null) {
      throw new CallError("no code at address");
    }
    const message = `sensitive-callback@${addressHex}`;
    this.sensitiveLog.push(message);
    return message;
  }
}
