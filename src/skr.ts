import { createHash, publicEncrypt, constants } from "node:crypto";
import {
  evaluatePolicy,
  getClaim,
  type Claims,
  type Policy,
} from "./policy.ts";

/**
 * 密钥释放:供密钥代理的释放接口调用。
 *
 * 流程:
 * 1. 用调用方注入的 verifyToken 验签并取出声明(验签失败即拒绝)。
 * 2. 对声明求值释放策略(allOf/anyOf 嵌套条件)。
 * 3. 校验 runtime-data 的 SHA-256 等于报告中的 REPORT_DATA,
 *    防止主机换掉令牌里声明的运行时公钥。
 * 4. 只用 runtime-data 里的公钥包裹密钥后返回。
 */

/** 调用方注入的令牌验签器:验签通过返回声明,否则返回 null。 */
export type TokenVerifier = (token: string) => Claims | null;

export interface ReleaseOptions {
  /** 释放策略。 */
  policy: Policy;
  /** 令牌验签器,由调用方注入。 */
  verifyToken: TokenVerifier;
  /** runtime-data 声明路径,默认 "runtime-data"。 */
  runtimeDataClaim?: string;
  /** REPORT_DATA 声明路径(hex 编码),默认 "report-data"。 */
  reportDataClaim?: string;
}

export type ReleaseResult =
  | { ok: true; wrappedKey: Buffer }
  | { ok: false; reason: string };

function sha256Hex(data: Buffer): string {
  return createHash("sha256").update(data).digest("hex");
}

/** 从 runtime-data JSON 中提取运行时公钥(PEM)。 */
function extractRuntimePublicKey(runtimeData: Buffer): string | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(runtimeData.toString("utf8"));
  } catch {
    return null;
  }
  if (
    parsed === null ||
    typeof parsed !== "object" ||
    typeof (parsed as { publicKey?: unknown }).publicKey !== "string"
  ) {
    return null;
  }
  return (parsed as { publicKey: string }).publicKey;
}

/**
 * 求值释放策略,通过则用 runtime-data 中的公钥包裹 key 并返回。
 * 任何一步失败都拒绝,不释放密钥材料。
 */
export function releaseKey(
  token: string,
  key: Buffer,
  options: ReleaseOptions,
): ReleaseResult {
  const runtimeDataClaim = options.runtimeDataClaim ?? "runtime-data";
  const reportDataClaim = options.reportDataClaim ?? "report-data";

  const claims = options.verifyToken(token);
  if (claims === null) {
    return { ok: false, reason: "token signature verification failed" };
  }

  if (!evaluatePolicy(options.policy, claims)) {
    return { ok: false, reason: "policy not satisfied" };
  }

  const runtimeDataB64 = getClaim(claims, runtimeDataClaim);
  if (typeof runtimeDataB64 !== "string") {
    return { ok: false, reason: "runtime-data claim missing or not a string" };
  }
  const runtimeData = Buffer.from(runtimeDataB64, "base64");

  const reportData = getClaim(claims, reportDataClaim);
  if (typeof reportData !== "string") {
    return { ok: false, reason: "report-data claim missing or not a string" };
  }

  // runtime-data 的哈希必须等于报告中的 REPORT_DATA,
  // 否则说明主机换掉了令牌声明的运行时数据(含公钥)。
  if (sha256Hex(runtimeData) !== reportData.toLowerCase()) {
    return { ok: false, reason: "runtime-data hash does not match REPORT_DATA" };
  }

  const publicKeyPem = extractRuntimePublicKey(runtimeData);
  if (publicKeyPem === null) {
    return { ok: false, reason: "runtime-data has no usable public key" };
  }

  const wrappedKey = publicEncrypt(
    {
      key: publicKeyPem,
      padding: constants.RSA_PKCS1_OAEP_PADDING,
      oaepHash: "sha256",
    },
    key,
  );
  return { ok: true, wrappedKey };
}
