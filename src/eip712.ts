import {
  concatBytes,
  hexToBytes,
  isAddress,
  isBytes32,
  keccak256,
  keccak256Hex,
  utf8Bytes,
} from "./hash.ts";

export interface Eip712Field {
  readonly name: string;
  readonly type: string;
}

export type Eip712Types = Record<string, readonly Eip712Field[]>;

export interface Eip712Domain {
  readonly name?: string;
  readonly version?: string;
  readonly chainId?: number | bigint | string;
  readonly verifyingContract?: string;
  readonly salt?: string;
}

export interface Eip712TypedData {
  readonly types: Eip712Types;
  readonly primaryType: string;
  readonly domain: Eip712Domain;
  readonly message: Record<string, unknown>;
}

export const RESPONSE_PROOF_PRIMARY_TYPE = "ResponseProof";

export const RESPONSE_PROOF_TYPE: readonly Eip712Field[] = [
  { name: "requestHash", type: "bytes32" },
  { name: "responseHash", type: "bytes32" },
  { name: "deployment", type: "bytes32" },
];

export const DEFAULT_RESPONSE_PROOF_DOMAIN: Eip712Domain = {
  name: "GraphGatewayResponseProof",
  version: "1",
};

export interface ResponseProofMessage {
  readonly requestHash: string;
  readonly responseHash: string;
  readonly deployment: string;
}

const DOMAIN_FIELD_ORDER = [
  "name",
  "version",
  "chainId",
  "verifyingContract",
  "salt",
] as const;

const DOMAIN_FIELD_TYPES: Record<(typeof DOMAIN_FIELD_ORDER)[number], string> = {
  name: "string",
  version: "string",
  chainId: "uint256",
  verifyingContract: "address",
  salt: "bytes32",
};

function assertPlainObject(
  value: unknown,
  label: string,
): asserts value is Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new TypeError(`${label} must be an object`);
  }
}

function typeDependencies(
  primaryType: string,
  types: Eip712Types,
  found: Set<string>,
): void {
  const fields = types[primaryType];
  if (!fields) {
    throw new TypeError(`Unknown EIP-712 type: ${primaryType}`);
  }

  for (const field of fields) {
    const baseType = field.type.replace(/(?:\[[^\]]*\])+$/, "");
    if (types[baseType] && baseType !== primaryType && !found.has(baseType)) {
      found.add(baseType);
      typeDependencies(baseType, types, found);
    }
  }
}

function encodeType(primaryType: string, types: Eip712Types): string {
  if (!types[primaryType]) {
    throw new TypeError(`Unknown EIP-712 type: ${primaryType}`);
  }

  const dependencies = new Set<string>();
  typeDependencies(primaryType, types, dependencies);
  const ordered = [primaryType, ...Array.from(dependencies).sort()];

  return ordered
    .map((typeName) => {
      const fields = types[typeName];
      if (!fields) {
        throw new TypeError(`Unknown EIP-712 type: ${typeName}`);
      }
      return `${typeName}(${fields
        .map((field) => `${field.type} ${field.name}`)
        .join(",")})`;
    })
    .join("");
}

function uint256Bytes(value: unknown, label: string): Uint8Array {
  let integer: bigint;
  try {
    integer = BigInt(value as string | number | bigint);
  } catch {
    throw new TypeError(`${label} must be an integer`);
  }
  if (integer < 0n || integer >= 1n << 256n) {
    throw new TypeError(`${label} is outside the uint256 range`);
  }

  const bytes = new Uint8Array(32);
  let remaining = integer;
  for (let index = 31; index >= 0; index -= 1) {
    bytes[index] = Number(remaining & 0xffn);
    remaining >>= 8n;
  }
  return bytes;
}

function addressBytes(value: unknown, label: string): Uint8Array {
  if (!isAddress(value)) {
    throw new TypeError(`${label} must be a 20-byte 0x-prefixed address`);
  }
  const bytes = new Uint8Array(32);
  bytes.set(hexToBytes(value), 12);
  return bytes;
}

function bytes32(value: unknown, label: string): Uint8Array {
  if (!isBytes32(value)) {
    throw new TypeError(`${label} must be a 32-byte 0x-prefixed value`);
  }
  return hexToBytes(value);
}

function encodeValue(
  type: string,
  value: unknown,
  label: string,
  types: Eip712Types,
): Uint8Array {
  if (type === "bytes32") {
    return bytes32(value, label);
  }
  if (type === "address") {
    return addressBytes(value, label);
  }
  if (type === "bool") {
    if (typeof value !== "boolean") {
      throw new TypeError(`${label} must be a boolean`);
    }
    return uint256Bytes(value ? 1 : 0, label);
  }
  if (type === "string") {
    if (typeof value !== "string") {
      throw new TypeError(`${label} must be a string`);
    }
    return keccak256(utf8Bytes(value));
  }
  if (type === "bytes") {
    if (typeof value !== "string") {
      throw new TypeError(`${label} must be a 0x-prefixed hex string`);
    }
    return keccak256(hexToBytes(value));
  }
  if (/^u?int(?:[0-9]+)?$/.test(type)) {
    return uint256Bytes(value, label);
  }
  if (types[type]) {
    return hashStruct(type, value, types);
  }
  throw new TypeError(`Unsupported EIP-712 field type: ${type}`);
}

function encodeData(
  primaryType: string,
  data: unknown,
  types: Eip712Types,
): Uint8Array {
  assertPlainObject(data, `${primaryType} value`);
  const fields = types[primaryType];
  if (!fields) {
    throw new TypeError(`Unknown EIP-712 type: ${primaryType}`);
  }

  return concatBytes(
    ...fields.map((field) =>
      encodeValue(
        field.type,
        data[field.name],
        `${primaryType}.${field.name}`,
        types,
      ),
    ),
  );
}

export function hashStruct(
  primaryType: string,
  data: unknown,
  types: Eip712Types,
): Uint8Array {
  const typeHash = keccak256(utf8Bytes(encodeType(primaryType, types)));
  return keccak256(concatBytes(typeHash, encodeData(primaryType, data, types)));
}

function domainTypes(domain: Eip712Domain): Eip712Types {
  const fields = DOMAIN_FIELD_ORDER.filter(
    (field) => domain[field] !== undefined,
  ).map((field) => ({
    name: field,
    type: DOMAIN_FIELD_TYPES[field],
  }));

  return { EIP712Domain: fields };
}

export function hashDomain(domain: Eip712Domain): Uint8Array {
  return hashStruct("EIP712Domain", domain, domainTypes(domain));
}

export function hashTypedData(typedData: Eip712TypedData): Uint8Array {
  const domainSeparator = hashDomain(typedData.domain);
  const messageHash = hashStruct(
    typedData.primaryType,
    typedData.message,
    typedData.types,
  );
  return keccak256(
    concatBytes(new Uint8Array([0x19, 0x01]), domainSeparator, messageHash),
  );
}

export function hashTypedDataHex(typedData: Eip712TypedData): string {
  return keccak256Hex(hashTypedData(typedData));
}

export function buildResponseProofTypedData(
  message: ResponseProofMessage,
  domain: Eip712Domain = DEFAULT_RESPONSE_PROOF_DOMAIN,
): Eip712TypedData {
  return {
    types: {
      [RESPONSE_PROOF_PRIMARY_TYPE]: RESPONSE_PROOF_TYPE,
    },
    primaryType: RESPONSE_PROOF_PRIMARY_TYPE,
    domain,
    message: {
      requestHash: message.requestHash,
      responseHash: message.responseHash,
      deployment: message.deployment,
    },
  };
}
