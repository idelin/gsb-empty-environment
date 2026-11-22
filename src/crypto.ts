import {
  createPrivateKey,
  createPublicKey,
  generateKeyPairSync,
  randomUUID,
  sign as nodeSign,
  verify as nodeVerify,
} from "node:crypto";
import type { IdentityId, SignedFact, FactPayload, FactKind, FactId } from "./types.ts";
import { factSigningInput, sha256Hex } from "./canonical.ts";

export interface IdentityKeyPair {
  readonly identity: IdentityId;
  readonly privateKey: string;
  readonly publicKey: string;
}

export interface SignatureService {
  /** Generate a fresh Ed25519 identity: fingerprint plus PEM key pair. */
  generateIdentity(): IdentityKeyPair;
  /** Sign and wrap a payload, producing a self-contained {@link SignedFact}. */
  signFact<F extends FactPayload>(
    pair: IdentityKeyPair,
    kind: FactKind,
    payload: F,
    options?: { factId?: FactId; signedAt?: number },
  ): SignedFact<F>;
  /**
   * Verify that the envelope signature is valid for the claimed signer.
   * The signer id must be the fingerprint of the signing key itself.
   */
  verifyFact(fact: SignedFact): Promise<boolean>;
  /** Register a public key so other nodes can verify its signatures. */
  registerPublicKey(identity: IdentityId, publicKey: string): void;
}

const ED25519 = "ed25519";

export class Ed25519SignatureService implements SignatureService {
  readonly #keys = new Map<IdentityId, string>();

  generateIdentity(): IdentityKeyPair {
    const { privateKey: privateKeyObject, publicKey: publicKeyObject } = generateKeyPairSync(ED25519);
    const privateKey = privateKeyObject.export({ type: "pkcs8", format: "pem" }).toString();
    const publicKey = publicKeyObject.export({ type: "spki", format: "pem" }).toString();
    const identity = sha256Hex(publicKey);
    this.#keys.set(identity, publicKey);
    return { identity, privateKey, publicKey };
  }

  registerPublicKey(identity: IdentityId, publicKey: string): void {
    if (sha256Hex(publicKey) !== identity) {
      throw new Error("public key does not match identity fingerprint");
    }
    this.#keys.set(identity, publicKey);
  }

  signFact<F extends FactPayload>(
    pair: IdentityKeyPair,
    kind: FactKind,
    payload: F,
    options: { factId?: FactId; signedAt?: number } = {},
  ): SignedFact<F> {
    if (!this.#keys.has(pair.identity)) {
      this.registerPublicKey(pair.identity, pair.publicKey);
    }
    const fact: SignedFact<F> = {
      factId: options.factId ?? randomUUID(),
      kind,
      payload,
      signer: pair.identity,
      signedAt: options.signedAt ?? Date.now(),
      signature: "",
    };
    const signature = nodeSign(
      null,
      Buffer.from(factSigningInput(fact), "utf8"),
      createPrivateKey(pair.privateKey),
    ).toString("base64");
    return { ...fact, signature };
  }

  async verifyFact(fact: SignedFact): Promise<boolean> {
    const publicKeyPem = this.#keys.get(fact.signer);
    if (!publicKeyPem) {
      return false;
    }
    if (sha256Hex(publicKeyPem) !== fact.signer) {
      return false;
    }
    const { signature: _signature, ...unsigned } = fact;
    const expected = factSigningInput(unsigned);
    try {
      return nodeVerify(
        null,
        Buffer.from(expected, "utf8"),
        createPublicKey(publicKeyPem),
        Buffer.from(fact.signature, "base64"),
      );
    } catch {
      return false;
    }
  }
}
