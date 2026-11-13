import { concatBytes } from "./bytes.js";
import { hashStruct } from "./encoding.js";
import type {
  Keccak256Fn,
  TypedDataTypes,
  TypedDataValue,
} from "./types.js";

/**
 * EIP-712 域对象。仅这四个字段会被编码，且按 name → version →
 * chainId → verifyingContract 的声明顺序进入类型字符串。
 */
export interface EIP712Domain {
  name?: string;
  version?: string;
  chainId?: number | bigint | string;
  verifyingContract?: string;
}

export const DOMAIN_TYPE_NAME = "EIP712Domain";

/**
 * 依据实际出现的字段构造域类型定义。未声明的字段不会出现在
 * 类型字符串与编码结果中（符合 EIP-712 “只含声明出现的字段”要求）。
 */
export function buildDomainTypes(domain: EIP712Domain): TypedDataTypes {
  const fields: { name: string; type: string }[] = [];
  if (domain.name !== undefined) fields.push({ name: "name", type: "string" });
  if (domain.version !== undefined) {
    fields.push({ name: "version", type: "string" });
  }
  if (domain.chainId !== undefined) {
    fields.push({ name: "chainId", type: "uint256" });
  }
  if (domain.verifyingContract !== undefined) {
    fields.push({ name: "verifyingContract", type: "address" });
  }
  return { [DOMAIN_TYPE_NAME]: fields };
}

function toDomainValue(domain: EIP712Domain): Record<string, TypedDataValue> {
  const value: Record<string, TypedDataValue> = {};
  if (domain.name !== undefined) value.name = domain.name;
  if (domain.version !== undefined) value.version = domain.version;
  if (domain.chainId !== undefined) value.chainId = domain.chainId;
  if (domain.verifyingContract !== undefined) {
    value.verifyingContract = domain.verifyingContract;
  }
  return value;
}

/** 域分隔符 = hashStruct(EIP712Domain)。 */
export function hashDomain(
  keccak256: Keccak256Fn,
  domain: EIP712Domain,
): Uint8Array {
  const domainTypes = buildDomainTypes(domain);
  return hashStruct(
    keccak256,
    domainTypes,
    DOMAIN_TYPE_NAME,
    toDomainValue(domain),
  );
}

const EIP191_PREFIX = new Uint8Array([0x19, 0x01]);

/**
 * 消息哈希 = keccak256(0x19 0x01 ‖ 域分隔符 ‖ hashStruct(message))。
 */
export function hashTypedData(
  keccak256: Keccak256Fn,
  domain: EIP712Domain,
  types: TypedDataTypes,
  primaryType: string,
  message: Record<string, TypedDataValue>,
): Uint8Array {
  const domainSeparator = hashDomain(keccak256, domain);
  const messageHash = hashStruct(keccak256, types, primaryType, message);
  return keccak256(concatBytes([EIP191_PREFIX, domainSeparator, messageHash]));
}
