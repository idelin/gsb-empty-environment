import { assertLen, concat, sha256, u64be, utf8 } from "./bytes.ts";

export const ADDRESS_LEN = 20;
export const SALT_LEN = 32;
export const DIGEST_LEN = 32;
export const CHAIN_ID_LEN = 8;
export const GENERATION_LEN = 8;

/**
 * 题内确定性地址：
 *   address = SHA256(0xff || deployer[20] || salt[32] || SHA256(initCode)[32]) 的后 20 字节
 * 所有字段定长，长度不符直接抛错。
 */
export function deriveAddress(
  deployer: Uint8Array,
  salt: Uint8Array,
  initCodeDigest: Uint8Array,
): Uint8Array {
  assertLen(deployer, ADDRESS_LEN, "deployer");
  assertLen(salt, SALT_LEN, "salt");
  assertLen(initCodeDigest, DIGEST_LEN, "initCodeDigest");
  return sha256(Uint8Array.of(0xff), deployer, salt, initCodeDigest).slice(
    DIGEST_LEN - ADDRESS_LEN,
  );
}

/**
 * 部署记录：预授权只能针对完整记录签发，不能只存地址。
 * 绑定 chainID、deployer、salt、initCode 摘要、预期 runtimeCode 摘要、
 * 构造参数摘要与部署代次。
 */
export interface DeploymentRecord {
  chainId: number;
  deployer: Uint8Array;
  salt: Uint8Array;
  initCodeDigest: Uint8Array;
  expectedRuntimeCodeDigest: Uint8Array;
  constructorArgsDigest: Uint8Array;
  generation: number;
}

export function validateRecord(record: DeploymentRecord): void {
  u64be(record.chainId, "chainId");
  u64be(record.generation, "generation");
  assertLen(record.deployer, ADDRESS_LEN, "deployer");
  assertLen(record.salt, SALT_LEN, "salt");
  assertLen(record.initCodeDigest, DIGEST_LEN, "initCodeDigest");
  assertLen(
    record.expectedRuntimeCodeDigest,
    DIGEST_LEN,
    "expectedRuntimeCodeDigest",
  );
  assertLen(record.constructorArgsDigest, DIGEST_LEN, "constructorArgsDigest");
}

/** 定长规范编码：4 + 8 + 20 + 32*4 + 8 = 168 字节。 */
export function encodeRecord(record: DeploymentRecord): Uint8Array {
  validateRecord(record);
  return concat(
    utf8("DREC"),
    u64be(record.chainId, "chainId"),
    record.deployer,
    record.salt,
    record.initCodeDigest,
    record.expectedRuntimeCodeDigest,
    record.constructorArgsDigest,
    u64be(record.generation, "generation"),
  );
}

export function recordDigest(record: DeploymentRecord): Uint8Array {
  return sha256(encodeRecord(record));
}

export function recordAddress(record: DeploymentRecord): Uint8Array {
  return deriveAddress(record.deployer, record.salt, record.initCodeDigest);
}

/**
 * 授权回执：由授权方对完整记录签发，tag 绑定 chainID 与记录摘要，
 * 不可跨 chainID 复制（宿主会同时校验 chainId 字段与 tag）。
 */
export interface Receipt {
  chainId: number;
  recordDigest: Uint8Array;
  tag: Uint8Array;
}

export class Authority {
  readonly secret: Uint8Array;

  constructor(secret: Uint8Array) {
    if (secret.length < 16) throw new Error("authority secret too short");
    this.secret = secret;
  }

  issue(record: DeploymentRecord): Receipt {
    const digest = recordDigest(record);
    return {
      chainId: record.chainId,
      recordDigest: digest,
      tag: this.tag(record.chainId, digest),
    };
  }

  verify(receipt: Receipt): boolean {
    if (!Number.isSafeInteger(receipt.chainId) || receipt.chainId < 0) {
      return false;
    }
    try {
      assertLen(receipt.recordDigest, DIGEST_LEN, "recordDigest");
      assertLen(receipt.tag, DIGEST_LEN, "tag");
    } catch {
      return false;
    }
    const expected = this.tag(receipt.chainId, receipt.recordDigest);
    return expected.every((b, i) => b === receipt.tag[i]);
  }

  private tag(chainId: number, digest: Uint8Array): Uint8Array {
    return sha256(
      this.secret,
      utf8("PREAUTH-V1"),
      u64be(chainId, "chainId"),
      digest,
    );
  }
}
