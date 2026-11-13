/**
 * 签名验证后端抽象。
 *
 * 证书签名算法由部署环境决定（Signal 生产环境使用 Ed25519，也可使用
 * ECDSA P-256 等）。校验包不绑定具体算法，而是通过该接口注入；
 * 默认提供基于平台 Web Crypto 的 ECDSA P-256 + SHA-256 实现。
 */
export interface SignatureVerifier {
  readonly algorithm: string;
  verify(
    publicKey: Uint8Array,
    signature: Uint8Array,
    data: Uint8Array,
  ): Promise<boolean>;
}

/** 默认签名验证器：ECDSA P-256 + SHA-256（SPKI 公钥，IEEE P1363 签名）。 */
export class EcdsaP256Sha256Verifier implements SignatureVerifier {
  public readonly algorithm = "ECDSA_P256_SHA256";

  async verify(
    publicKey: Uint8Array,
    signature: Uint8Array,
    data: Uint8Array,
  ): Promise<boolean> {
    const key = await globalThis.crypto.subtle.importKey(
      "spki",
      publicKey,
      { name: "ECDSA", namedCurve: "P-256" },
      false,
      ["verify"],
    );
    try {
      return await globalThis.crypto.subtle.verify(
        { name: "ECDSA", hash: "SHA-256" },
        key,
        signature,
        data,
      );
    } catch {
      return false;
    }
  }
}

export const defaultSignatureVerifier: SignatureVerifier =
  new EcdsaP256Sha256Verifier();
