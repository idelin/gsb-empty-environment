export {
  arrayBaseType,
  encodeData,
  encodeType,
  findDependencies,
  fixedArrayLength,
  hashStruct,
  isAtomicType,
  typeHash,
} from "./encoding.js";
export {
  buildDomainTypes,
  DOMAIN_TYPE_NAME,
  hashDomain,
  hashTypedData,
  type EIP712Domain,
} from "./domain.js";
export {
  parseSignature,
  SECP256K1_HALF_N,
  SECP256K1_N,
  verifyTypedData,
  type ParsedSignature,
  type VerifyDependencies,
  type VerifyOptions,
} from "./signature.js";
export {
  InMemoryNonceStore,
  type NonceStore,
} from "./nonce.js";
export {
  TypedDataRelayer,
  type RelayAcceptedCallback,
  type RelayRequest,
  type RelayResult,
  type RelayerConfig,
} from "./relayer.js";
export {
  SignatureVerificationError,
  VerificationErrorCode,
} from "./errors.js";
export {
  bytesToHex,
  concatBytes,
  encodeAddress,
  hexToBytes,
  isHexString,
  normalizeAddress,
  uintToBytes32,
  utf8ToBytes,
  type HexString,
} from "./bytes.js";
export type {
  Keccak256Fn,
  RecoverAddressFn,
  TypedDataField,
  TypedDataMessage,
  TypedDataTypes,
  TypedDataValue,
} from "./types.js";
