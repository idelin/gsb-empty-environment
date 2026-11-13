export interface KbJwtParts {
  header: Record<string, unknown>;
  payload: Record<string, unknown>;
  signature: string;
}

export interface VerifierChallenge {
  /** 本次验证方标识 */
  audience: string;
  /** 本次挑战 */
  nonce: string;
}

export type VerifyResult =
  | { valid: true }
  | { valid: false; reason: "aud_mismatch" | "nonce_mismatch" | "malformed" };

function b64urlDecode(segment: string): string {
  const b64 = segment.replace(/-/g, "+").replace(/_/g, "/");
  return Buffer.from(b64, "base64").toString("utf8");
}

function b64urlEncode(text: string): string {
  return Buffer.from(text, "utf8")
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

export function parseKbJwt(kbJwt: string): KbJwtParts {
  const parts = kbJwt.split(".");
  if (parts.length !== 3 || parts.some((p) => p.length === 0)) {
    throw new Error("malformed kb-jwt: expected header.payload.signature");
  }
  const [headerSeg, payloadSeg, signature] = parts;
  let header: Record<string, unknown>;
  let payload: Record<string, unknown>;
  try {
    header = JSON.parse(b64urlDecode(headerSeg));
    payload = JSON.parse(b64urlDecode(payloadSeg));
  } catch {
    throw new Error("malformed kb-jwt: header/payload is not valid JSON");
  }
  return { header, payload, signature };
}

/**
 * 钱包调用：保持原签名不变，仅将 kb-jwt 的 aud 替换为目标验证方标识。
 */
export function replaceAudience(kbJwt: string, audience: string): string {
  const parts = kbJwt.split(".");
  if (parts.length !== 3 || parts.some((p) => p.length === 0)) {
    throw new Error("malformed kb-jwt: expected header.payload.signature");
  }
  const [headerSeg, payloadSeg, signature] = parts;
  let payload: Record<string, unknown>;
  try {
    payload = JSON.parse(b64urlDecode(payloadSeg));
  } catch {
    throw new Error("malformed kb-jwt: payload is not valid JSON");
  }
  payload.aud = audience;
  return `${headerSeg}.${b64urlEncode(JSON.stringify(payload))}.${signature}`;
}

/**
 * 验证方：aud 必须等于本次验证方标识，nonce 必须等于本次挑战；
 * 任一不符则整份出示无效，两者皆对才接受。
 */
export function verifyPresentation(
  kbJwt: string,
  challenge: VerifierChallenge,
): VerifyResult {
  let parsed: KbJwtParts;
  try {
    parsed = parseKbJwt(kbJwt);
  } catch {
    return { valid: false, reason: "malformed" };
  }
  if (parsed.payload.aud !== challenge.audience) {
    return { valid: false, reason: "aud_mismatch" };
  }
  if (parsed.payload.nonce !== challenge.nonce) {
    return { valid: false, reason: "nonce_mismatch" };
  }
  return { valid: true };
}
