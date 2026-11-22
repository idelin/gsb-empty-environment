import { canonicalJson, deepFreeze, hashDomain, randomId } from "./canonical.ts";
import {
  BROWSER_PROOF_DOMAIN,
  CONFIRMATION_DOMAIN,
  RECEIPT_DOMAIN,
  signPayload,
  verifyPayload,
} from "./keys.ts";
import {
  emailTokenHash,
  parseEmailToken,
  verifyBrowserProof,
  verifyDeviceConfirmation,
  verifyEmailToken,
} from "./messages.ts";
import { deriveTransactionId } from "./protocol-ids.ts";
import type {
  BrowserProof,
  DeviceConfirmation,
  EmailToken,
  HostKeyPair,
  IssuedCredential,
  OfflineAcceptanceResult,
  ReceiptType,
  SignedReceipt,
} from "./types.ts";

const RECEIPT_HASH_DOMAIN = "ddd-cross-device/receipt-hash/v1";

export interface ReceiptInput {
  type: ReceiptType;
  payload: Record<string, unknown>;
  issuedAt: number;
  prevReceiptHash: string | null;
  signer: HostKeyPair;
}

export function createReceipt(input: ReceiptInput): SignedReceipt {
  const unsigned = {
    v: 1 as const,
    receiptId: randomId("rcpt"),
    type: input.type,
    issuedAt: input.issuedAt,
    prevReceiptHash: input.prevReceiptHash,
    payload: structuredClone(input.payload),
  };
  const hash = hashDomain(RECEIPT_HASH_DOMAIN, unsigned);
  const signature = signPayload(input.signer.privateKey, RECEIPT_DOMAIN, { hash });
  return deepFreeze({ ...unsigned, hash, signature });
}

export function verifyReceipt(receipt: SignedReceipt, hostPublicKey: string): boolean {
  try {
    if (receipt.v !== 1) return false;
    const unsigned = {
      v: receipt.v,
      receiptId: receipt.receiptId,
      type: receipt.type,
      issuedAt: receipt.issuedAt,
      prevReceiptHash: receipt.prevReceiptHash,
      payload: receipt.payload,
    };
    if (hashDomain(RECEIPT_HASH_DOMAIN, unsigned) !== receipt.hash) return false;
    return verifyPayload(hostPublicKey, RECEIPT_DOMAIN, { hash: receipt.hash }, receipt.signature);
  } catch {
    return false;
  }
}

export function verifyReceiptChain(receipts: SignedReceipt[], hostPublicKey: string): boolean {
  let previous: string | null = null;
  for (const receipt of receipts) {
    if (!verifyReceipt(receipt, hostPublicKey)) return false;
    if (receipt.prevReceiptHash !== previous) return false;
    previous = receipt.hash;
  }
  return true;
}

export function verifyEmailDeliveryPair(
  dispatch: SignedReceipt,
  delivery: SignedReceipt,
  hostPublicKey: string,
): { accepted: boolean; reason?: string; tokenId?: string; challengeId?: string } {
  if (!verifyReceipt(dispatch, hostPublicKey)) {
    return { accepted: false, reason: "invalid_dispatch_receipt" };
  }
  if (!verifyReceipt(delivery, hostPublicKey)) {
    return { accepted: false, reason: "invalid_delivery_receipt" };
  }
  if (dispatch.type !== "email.dispatch" || delivery.type !== "email.delivery") {
    return { accepted: false, reason: "wrong_receipt_type" };
  }
  const dispatchPayload = dispatch.payload;
  const deliveryPayload = delivery.payload;
  for (const payload of [dispatchPayload, deliveryPayload]) {
    if (
      typeof payload.tokenId !== "string" ||
      typeof payload.challengeId !== "string" ||
      typeof payload.tokenHash !== "string"
    ) {
      return { accepted: false, reason: "malformed_delivery_receipt" };
    }
  }
  if (dispatchPayload.tokenId !== deliveryPayload.tokenId) {
    return { accepted: false, reason: "token_mismatch" };
  }
  if (dispatchPayload.challengeId !== deliveryPayload.challengeId) {
    return { accepted: false, reason: "challenge_mismatch" };
  }
  if (dispatchPayload.tokenHash !== deliveryPayload.tokenHash) {
    return { accepted: false, reason: "token_hash_mismatch" };
  }
  if (deliveryPayload.dispatchReceiptHash !== dispatch.hash) {
    return { accepted: false, reason: "dispatch_link_mismatch" };
  }
  if (
    typeof delivery.issuedAt !== "number" ||
    typeof dispatch.issuedAt !== "number" ||
    delivery.issuedAt < dispatch.issuedAt
  ) {
    return { accepted: false, reason: "delivery_time_invalid" };
  }
  return {
    accepted: true,
    tokenId: dispatchPayload.tokenId as string,
    challengeId: dispatchPayload.challengeId as string,
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object";
}

export function verifyIssuedAuthorization(
  receipt: SignedReceipt,
  hostPublicKey: string,
): OfflineAcceptanceResult {
  if (!verifyReceipt(receipt, hostPublicKey)) {
    return { accepted: false, reason: "invalid_receipt" };
  }
  if (receipt.type !== "authorization.issued") {
    return { accepted: false, reason: "wrong_receipt_type" };
  }
  const payload = receipt.payload;
  if (!isRecord(payload.token) || !isRecord(payload.confirmation) || !isRecord(payload.browserProof)) {
    return { accepted: false, reason: "missing_evidence" };
  }
  if (!isRecord(payload.credential)) {
    return { accepted: false, reason: "missing_credential" };
  }

  let token: EmailToken;
  try {
    token = parseEmailToken(payload.token as unknown as EmailToken);
  } catch {
    return { accepted: false, reason: "malformed_token" };
  }
  if (!verifyEmailToken(token, hostPublicKey)) {
    return { accepted: false, reason: "invalid_token_signature" };
  }
  if (emailTokenHash(token) !== payload.tokenHash) {
    return { accepted: false, reason: "token_hash_mismatch" };
  }
  const claims = token.claims;
  if (
    payload.accountId !== claims.accountId ||
    payload.challengeId !== claims.challengeId ||
    payload.tokenId !== claims.tokenId ||
    payload.browserId !== claims.browserId ||
    payload.browserSessionId !== claims.browserSessionId ||
    payload.browserKeyId !== claims.browserKeyId ||
    payload.purpose !== claims.purpose ||
    payload.expiresAt !== claims.expiresAt
  ) {
    return { accepted: false, reason: "token_binding_mismatch" };
  }

  const confirmation = payload.confirmation as unknown as DeviceConfirmation;
  if (!verifyDeviceConfirmation(confirmation)) {
    return { accepted: false, reason: "invalid_device_confirmation" };
  }
  if (
    confirmation.payload.accountId !== claims.accountId ||
    confirmation.payload.tokenId !== claims.tokenId ||
    confirmation.payload.challengeId !== claims.challengeId ||
    confirmation.payload.challenge !== claims.challenge ||
    confirmation.payload.expiresAt !== claims.expiresAt ||
    confirmation.payload.purpose !== claims.purpose ||
    confirmation.payload.browserId !== claims.browserId ||
    confirmation.payload.browserSessionId !== claims.browserSessionId ||
    confirmation.payload.browserKeyId !== claims.browserKeyId ||
    confirmation.payload.selectedBrowserId !== claims.browserId ||
    confirmation.payload.selectedBrowserSessionId !== claims.browserSessionId
  ) {
    return { accepted: false, reason: "confirmation_binding_mismatch" };
  }
  if (hashDomain(CONFIRMATION_DOMAIN, confirmation.payload) !== payload.confirmationHash) {
    return { accepted: false, reason: "confirmation_hash_mismatch" };
  }

  const proof = payload.browserProof as unknown as BrowserProof;
  if (!verifyBrowserProof(proof, claims.browserPublicKey)) {
    return { accepted: false, reason: "invalid_browser_proof" };
  }
  if (
    proof.payload.tokenId !== claims.tokenId ||
    proof.payload.challengeId !== claims.challengeId ||
    proof.payload.challenge !== claims.challenge ||
    proof.payload.accountId !== claims.accountId ||
    proof.payload.browserId !== claims.browserId ||
    proof.payload.browserSessionId !== claims.browserSessionId ||
    proof.payload.browserKeyId !== claims.browserKeyId ||
    proof.payload.purpose !== claims.purpose
  ) {
    return { accepted: false, reason: "proof_binding_mismatch" };
  }
  if (hashDomain(BROWSER_PROOF_DOMAIN, proof.payload) !== payload.proofHash) {
    return { accepted: false, reason: "proof_hash_mismatch" };
  }

  const credential = payload.credential as unknown as IssuedCredential;
  if (credential.transactionId !== payload.transactionId) {
    return { accepted: false, reason: "credential_transaction_mismatch" };
  }
  if (
    credential.accountId !== claims.accountId ||
    credential.browserId !== claims.browserId ||
    credential.browserSessionId !== claims.browserSessionId ||
    credential.browserKeyId !== claims.browserKeyId ||
    credential.purpose !== claims.purpose
  ) {
    return { accepted: false, reason: "credential_binding_mismatch" };
  }
  if (hashDomain("ddd-cross-device/issued-credential/v1", credential) !== payload.credentialHash) {
    return { accepted: false, reason: "credential_hash_mismatch" };
  }
  if (credential.expiresAt < credential.issuedAt) {
    return { accepted: false, reason: "credential_time_invalid" };
  }
  if (deriveTransactionId(claims) !== payload.transactionId) {
    return { accepted: false, reason: "transaction_id_mismatch" };
  }
  if (
    typeof payload.authorizedAt !== "number" ||
    payload.authorizedAt >= claims.expiresAt
  ) {
    return { accepted: false, reason: "authorization_time_invalid" };
  }

  return {
    accepted: true,
    tokenId: claims.tokenId,
    challengeId: claims.challengeId,
    accountId: claims.accountId,
    browserId: claims.browserId,
    browserSessionId: claims.browserSessionId,
    browserKeyId: claims.browserKeyId,
    purpose: claims.purpose,
    transactionId: credential.transactionId,
    credentialId: credential.credentialId,
    receiptHash: receipt.hash,
  };
}

export function receiptCanonicalForm(receipt: SignedReceipt): string {
  return canonicalJson(receipt);
}
