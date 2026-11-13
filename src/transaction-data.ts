import { createHash, verify, type KeyObject } from "node:crypto";

export type HashAlgorithm = "sha-256" | "sha-384" | "sha-512";

const NODE_DIGEST: Record<HashAlgorithm, string> = {
  "sha-256": "sha256",
  "sha-384": "sha384",
  "sha-512": "sha512",
};

export interface PresentationCheck {
  allowed: boolean;
  reason: string;
}

export interface VerifyPresentationArgs {
  /** 当前出示时附带的交易数据字节（必须与钱包展示给用户的字节一致）。 */
  presentedTransactionData: Uint8Array;
  /** 持有方密钥绑定 JWT（kb-jwt），其 payload 应携带 transaction_data_hashes。 */
  kbJwt?: string;
  /** 设备签名，覆盖交易数据哈希。 */
  deviceSignature?: Uint8Array;
  /** 设备公钥，用于校验 deviceSignature。 */
  devicePublicKey?: KeyObject;
  hashAlgorithm?: HashAlgorithm;
}

/** 计算交易数据字节的哈希。 */
export function hashTransactionData(
  data: Uint8Array,
  alg: HashAlgorithm = "sha-256",
): Uint8Array {
  return new Uint8Array(createHash(NODE_DIGEST[alg]).update(data).digest());
}

function base64UrlDecode(input: string): Uint8Array {
  const b64 = input.replace(/-/g, "+").replace(/_/g, "/");
  return new Uint8Array(Buffer.from(b64, "base64"));
}

function base64UrlEncode(data: Uint8Array): string {
  return Buffer.from(data)
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

/**
 * 从 kb-jwt payload 中解析交易数据哈希（OpenID4VP 的
 * transaction_data_hashes / transaction_data_hashes_alg 声明）。
 * 仅解码，不校验 JWT 签名——调用方（钱包）应已完成签名验证。
 * 缺失时返回 null。
 */
export function extractTransactionDataHashes(kbJwt: string): {
  hashes: Uint8Array[];
  alg: string;
} | null {
  const parts = kbJwt.split(".");
  if (parts.length !== 3) return null;
  let payload: Record<string, unknown>;
  try {
    payload = JSON.parse(Buffer.from(base64UrlDecode(parts[1])).toString("utf8"));
  } catch {
    return null;
  }
  const raw = payload["transaction_data_hashes"];
  if (!Array.isArray(raw) || raw.length === 0) return null;
  const rawAlg = payload["transaction_data_hashes_alg"];
  const alg = typeof rawAlg === "string" ? rawAlg : "sha-256";
  const hashes: Uint8Array[] = [];
  for (const entry of raw) {
    if (typeof entry !== "string") return null;
    hashes.push(base64UrlDecode(entry));
  }
  return { hashes, alg };
}

/** 构造一个携带交易数据哈希声明的 kb-jwt（供测试/演示组装）。 */
export function buildKbJwt(
  transactionDataHashes: Uint8Array[],
  alg: HashAlgorithm = "sha-256",
  extraClaims: Record<string, unknown> = {},
): string {
  const header = base64UrlEncode(
    new TextEncoder().encode(JSON.stringify({ alg: "ES256", typ: "kb+jwt" })),
  );
  const payload = base64UrlEncode(
    new TextEncoder().encode(
      JSON.stringify({
        ...extraClaims,
        transaction_data_hashes: transactionDataHashes.map(base64UrlEncode),
        transaction_data_hashes_alg: alg,
      }),
    ),
  );
  return `${header}.${payload}.`;
}

function hashesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i] ^ b[i];
  return diff === 0;
}

/**
 * 出示前校验：kb-jwt 或设备签名必须覆盖当前交易数据字节的哈希。
 * 哈希不符或缺失时拒绝出示；哈希覆盖当前交易字节时允许出示。
 */
export function verifyPresentation(args: VerifyPresentationArgs): PresentationCheck {
  const alg = args.hashAlgorithm ?? "sha-256";
  const currentHash = hashTransactionData(args.presentedTransactionData, alg);

  let covered = false;

  if (args.kbJwt !== undefined) {
    const extracted = extractTransactionDataHashes(args.kbJwt);
    if (extracted === null) {
      return { allowed: false, reason: "kb-jwt 缺失 transaction_data_hashes 声明" };
    }
    if (extracted.alg !== alg) {
      return {
        allowed: false,
        reason: `kb-jwt 哈希算法不符：期望 ${alg}，实际 ${extracted.alg}`,
      };
    }
    if (extracted.hashes.some((h) => hashesEqual(h, currentHash))) {
      covered = true;
    } else {
      return { allowed: false, reason: "kb-jwt 中的哈希不覆盖当前交易数据字节" };
    }
  }

  if (args.deviceSignature !== undefined || args.devicePublicKey !== undefined) {
    if (args.deviceSignature === undefined || args.devicePublicKey === undefined) {
      return { allowed: false, reason: "设备签名与设备公钥必须同时提供" };
    }
    const ok = verify(
      null,
      Buffer.from(currentHash),
      args.devicePublicKey,
      Buffer.from(args.deviceSignature),
    );
    if (!ok) {
      return { allowed: false, reason: "设备签名未覆盖当前交易数据哈希" };
    }
    covered = true;
  }

  if (!covered) {
    return { allowed: false, reason: "未提供覆盖交易数据哈希的 kb-jwt 或设备签名" };
  }
  return { allowed: true, reason: "哈希覆盖当前交易字节，允许出示" };
}
