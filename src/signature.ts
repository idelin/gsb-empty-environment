import { bytesToHex, hexToBytes, normalizeAddress } from "./bytes.js";
import { hashTypedData, type EIP712Domain } from "./domain.js";
import { SignatureVerificationError, VerificationErrorCode } from "./errors.js";
import type {
  Keccak256Fn,
  RecoverAddressFn,
  TypedDataTypes,
  TypedDataValue,
} from "./types.js";

/** secp256k1 曲线阶 n。 */
export const SECP256K1_N =
  0xfffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141n;

/** 曲线阶的一半，s 必须落在低半区（EIP-2）。 */
export const SECP256K1_HALF_N =
  0x7fffffffffffffffffffffffffffffff5d576e7357a4501ddfe92f46681b20a0n;

export interface ParsedSignature {
  r: Uint8Array;
  s: Uint8Array;
  /** 恢复标识，已归一为 0/1。 */
  recoveryId: number;
  /** 65 字节 r || s || v（v 为 0/1），便于直接交给恢复函数。 */
  compact: Uint8Array;
}

/**
 * 解析 65 字节签名（hex 字符串或字节数组，布局 r||s||v）。
 * v 只接受 27/28（会归一为 0/1）；s 落在高半区直接拒绝。
 */
export function parseSignature(signature: string | Uint8Array): ParsedSignature {
  let bytes: Uint8Array;
  if (typeof signature === "string") {
    try {
      bytes = hexToBytes(signature);
    } catch {
      throw new SignatureVerificationError(
        VerificationErrorCode.INVALID_SIGNATURE_ENCODING,
        "signature must be a 0x-prefixed hex string",
      );
    }
  } else if (signature instanceof Uint8Array) {
    bytes = signature;
  } else {
    throw new SignatureVerificationError(
      VerificationErrorCode.INVALID_SIGNATURE_ENCODING,
      "signature must be a hex string or Uint8Array",
    );
  }

  if (bytes.length !== 65) {
    throw new SignatureVerificationError(
      VerificationErrorCode.INVALID_SIGNATURE_LENGTH,
      `signature must be 65 bytes, got ${bytes.length}`,
    );
  }

  const r = bytes.slice(0, 32);
  const s = bytes.slice(32, 64);
  const rawV = bytes[64];

  if (rawV !== 27 && rawV !== 28) {
    throw new SignatureVerificationError(
      VerificationErrorCode.INVALID_V,
      `signature v must be 27 or 28, got ${rawV}`,
    );
  }

  const sValue = BigInt(bytesToHex(s));
  if (sValue > SECP256K1_HALF_N) {
    throw new SignatureVerificationError(
      VerificationErrorCode.HIGH_S,
      "signature s value is in the upper half of the curve order (malleable)",
    );
  }
  if (sValue === 0n) {
    throw new SignatureVerificationError(
      VerificationErrorCode.INVALID_SIGNATURE_ENCODING,
      "signature s must not be zero",
    );
  }

  const rValue = BigInt(bytesToHex(r));
  if (rValue === 0n || rValue >= SECP256K1_N) {
    throw new SignatureVerificationError(
      VerificationErrorCode.INVALID_SIGNATURE_ENCODING,
      "signature r is out of range",
    );
  }

  const recoveryId = rawV - 27;
  const compact = new Uint8Array(65);
  compact.set(r, 0);
  compact.set(s, 32);
  compact[64] = recoveryId;

  return { r, s, recoveryId, compact };
}

export interface VerifyOptions {
  domain: EIP712Domain;
  types: TypedDataTypes;
  primaryType: string;
  message: Record<string, TypedDataValue>;
  /** 消息中声明的签名者地址。 */
  expectedSigner: string;
  signature: string | Uint8Array;
}

export interface VerifyDependencies {
  keccak256: Keccak256Fn;
  recoverAddress: RecoverAddressFn;
}

/**
 * 纯签名校验：解析并做 malleability 检查 → 计算 EIP-712 摘要 →
 * 恢复地址 → 与声明签名者比对。不涉及域绑定、nonce、deadline。
 */
export async function verifyTypedData(
  deps: VerifyDependencies,
  options: VerifyOptions,
): Promise<void> {
  const parsed = parseSignature(options.signature);
  const digest = hashTypedData(
    deps.keccak256,
    options.domain,
    options.types,
    options.primaryType,
    options.message,
  );
  const recovered = normalizeAddress(await deps.recoverAddress(digest, parsed.compact));
  const expected = normalizeAddress(options.expectedSigner);

  if (recovered !== expected) {
    throw new SignatureVerificationError(
      VerificationErrorCode.SIGNER_MISMATCH,
      `recovered signer ${recovered} does not match declared signer ${expected}`,
    );
  }
}
