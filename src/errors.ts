/**
 * 校验失败的机器可读错误码。所有失败路径都稳定抛出这些码，
 * 便于中继服务映射为固定的 API 响应。
 */
export const VerificationErrorCode = {
  INVALID_SIGNATURE_LENGTH: "INVALID_SIGNATURE_LENGTH",
  INVALID_SIGNATURE_ENCODING: "INVALID_SIGNATURE_ENCODING",
  INVALID_V: "INVALID_V",
  HIGH_S: "HIGH_S",
  SIGNER_MISMATCH: "SIGNER_MISMATCH",
  MISSING_CHAIN_ID: "MISSING_CHAIN_ID",
  CHAIN_ID_MISMATCH: "CHAIN_ID_MISMATCH",
  MISSING_VERIFYING_CONTRACT: "MISSING_VERIFYING_CONTRACT",
  VERIFYING_CONTRACT_MISMATCH: "VERIFYING_CONTRACT_MISMATCH",
  MISSING_NONCE: "MISSING_NONCE",
  INVALID_NONCE: "INVALID_NONCE",
  NONCE_MISMATCH: "NONCE_MISMATCH",
  MISSING_DEADLINE: "MISSING_DEADLINE",
  DEADLINE_EXPIRED: "DEADLINE_EXPIRED",
  MISSING_SIGNER: "MISSING_SIGNER",
} as const;

export type VerificationErrorCode =
  (typeof VerificationErrorCode)[keyof typeof VerificationErrorCode];

export class SignatureVerificationError extends Error {
  readonly code: VerificationErrorCode;

  constructor(code: VerificationErrorCode, message: string) {
    super(message);
    this.name = "SignatureVerificationError";
    this.code = code;
  }
}
