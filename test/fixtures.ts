import { bytesToHex } from "../src/bytes.js";
import { hashTypedData } from "../src/domain.js";
import type {
  EIP712Domain,
  TypedDataMessage,
  TypedDataTypes,
} from "../src/index.js";
import { keccak256 } from "./support/keccak.js";
import {
  addressFromPrivateKey,
  signDigest,
  signatureToBytes,
} from "./support/secp256k1.js";

export const CHAIN_ID = 1n;
export const OTHER_CHAIN_ID = 5n;
export const TARGET_CONTRACT = "0x1111111111111111111111111111111111111111";
export const OTHER_CONTRACT = "0x2222222222222222222222222222222222222222";
export const NOW = 1_700_000_000n;
export const FUTURE_DEADLINE = NOW + 3_600n;
export const PAST_DEADLINE = NOW - 1n;

export interface SignerIdentity {
  privateKey: bigint;
  address: string;
}

export function makeSigner(privateKey: bigint): SignerIdentity {
  return { privateKey, address: addressFromPrivateKey(privateKey) };
}

export const AUTHORIZATION_TYPES: TypedDataTypes = {
  Authorization: [
    { name: "signer", type: "address" },
    { name: "target", type: "address" },
    { name: "value", type: "uint256" },
    { name: "nonce", type: "uint256" },
    { name: "deadline", type: "uint256" },
    { name: "data", type: "bytes" },
  ],
};

export function buildDomain(
  overrides: Partial<EIP712Domain> = {},
): EIP712Domain {
  return {
    name: "PairwiseGSB",
    version: "1",
    chainId: CHAIN_ID,
    verifyingContract: TARGET_CONTRACT,
    ...overrides,
  };
}

export interface AuthorizationMessage extends TypedDataMessage {
  signer: string;
  target: string;
  value: bigint;
  nonce: bigint;
  deadline: bigint;
  data: string;
}

export function buildMessage(
  signer: SignerIdentity,
  overrides: Partial<AuthorizationMessage> = {},
): AuthorizationMessage {
  return {
    signer: signer.address,
    target: "0x3333333333333333333333333333333333333333",
    value: 1_000n,
    nonce: 0n,
    deadline: FUTURE_DEADLINE,
    data: "0xdeadbeef",
    ...overrides,
  };
}

export function digestFor(
  domain: EIP712Domain,
  message: AuthorizationMessage,
): Uint8Array {
  return hashTypedData(
    keccak256,
    domain,
    AUTHORIZATION_TYPES,
    "Authorization",
    message,
  );
}

/** 用私钥对消息签名，返回 0x 前缀的 65 字节签名（v 为 27/28，低 s）。 */
export function signAuthorization(
  signer: SignerIdentity,
  domain: EIP712Domain,
  message: AuthorizationMessage,
): string {
  const digest = digestFor(domain, message);
  const signature = signDigest(digest, signer.privateKey);
  return bytesToHex(signatureToBytes(signature));
}
