import {
  concatBytes,
  encodeAddress,
  hexToBytes,
  uintToBytes32,
  utf8ToBytes,
} from "./bytes.js";
import type {
  Keccak256Fn,
  TypedDataField,
  TypedDataTypes,
  TypedDataValue,
} from "./types.js";

const ADDRESS = "address";
const BOOL = "bool" as const;
const DYNAMIC_BYTES = "bytes";
const STRING = "string" as const;

/** 取数组元素的基础类型，非数组返回 null。同时识别定长与变长数组。 */
export function arrayBaseType(type: string): string | null {
  const match = /^(.*)\[\d*]$/.exec(type);
  return match ? match[1] : null;
}

/** 定长数组的声明长度；变长数组返回 null。 */
export function fixedArrayLength(type: string): number | null {
  const match = /^\S+\[(\d+)]$/.exec(type);
  return match ? Number(match[1]) : null;
}

/** bytes1..bytes32 定长字节类型。 */
function isFixedBytes(type: string): boolean {
  const match = /^bytes(\d+)$/.exec(type);
  if (!match) return false;
  const size = Number(match[1]);
  return size >= 1 && size <= 32;
}

/** uint8..uint256 / int8..int256（步进 8）。 */
function isInteger(type: string): boolean {
  const match = /^(u?int)(\d*)$/.exec(type);
  if (!match) return false;
  if (match[2] === "") return true;
  const bits = Number(match[2]);
  return bits >= 8 && bits <= 256 && bits % 8 === 0;
}

/** Solidity 原子（非结构）类型判定。 */
export function isAtomicType(type: string): boolean {
  return (
    type === BOOL ||
    type === ADDRESS ||
    type === DYNAMIC_BYTES ||
    type === STRING ||
    isFixedBytes(type) ||
    isInteger(type)
  );
}

/**
 * 收集 primaryType 引用到的全部结构类型（含自身），不包含 Solidity 原子类型。
 */
export function findDependencies(
  types: TypedDataTypes,
  primaryType: string,
): string[] {
  const found = new Set<string>();
  const visit = (typeName: string): void => {
    if (found.has(typeName) || isAtomicType(arrayBaseType(typeName) ?? typeName)) {
      return;
    }
    found.add(typeName);
    const fields = types[typeName];
    if (!fields) {
      throw new Error(`unknown struct type referenced: ${typeName}`);
    }
    for (const field of fields) {
      const base = arrayBaseType(field.type) ?? field.type;
      if (!isAtomicType(base)) {
        visit(base);
      }
    }
  };
  visit(primaryType);
  return [...found];
}

/**
 * 类型字符串：primaryType 的成员按声明顺序在前，
 * 其余依赖结构按结构名字典序排列。
 * 例如 "Mail(Person from,Person to,string contents)Person(string name,address wallet)"
 */
export function encodeType(
  types: TypedDataTypes,
  primaryType: string,
): string {
  const dependencies = findDependencies(types, primaryType);
  const ordered = [
    primaryType,
    ...dependencies
      .filter((name) => name !== primaryType)
      .sort((a, b) => (a < b ? -1 : a > b ? 1 : 0)),
  ];
  return ordered
    .map((name) => {
      const fields = types[name];
      if (!fields) {
        throw new Error(`unknown struct type referenced: ${name}`);
      }
      return `${name}(${fields
        .map((field: TypedDataField) => `${field.type} ${field.name}`)
        .join(",")})`;
    })
    .join("");
}

/** typeHash = keccak256(encodeType(primaryType))。 */
export function typeHash(
  keccak256: Keccak256Fn,
  types: TypedDataTypes,
  primaryType: string,
): Uint8Array {
  return keccak256(utf8ToBytes(encodeType(types, primaryType)));
}

function encodeBoolean(value: TypedDataValue): Uint8Array {
  const out = new Uint8Array(32);
  out[31] = value ? 1 : 0;
  return out;
}

function encodeFixedBytes(type: string, value: TypedDataValue): Uint8Array {
  const size = Number(/^bytes(\d+)$/.exec(type)?.[1] ?? "0");
  if (typeof value !== "string") {
    throw new Error(`${type} expects a hex string`);
  }
  const raw = hexToBytes(value);
  if (raw.length !== size) {
    throw new Error(`${type} expects exactly ${size} bytes, got ${raw.length}`);
  }
  const out = new Uint8Array(32);
  out.set(raw, 0);
  return out;
}

function encodeInteger(value: TypedDataValue): Uint8Array {
  if (typeof value === "number") {
    if (!Number.isSafeInteger(value)) {
      throw new Error(`integer is not a safe integer: ${String(value)}`);
    }
    return uintToBytes32(BigInt(value));
  }
  if (typeof value === "bigint") {
    return uintToBytes32(value);
  }
  if (typeof value === "string" && /^\d+$/.test(value)) {
    return uintToBytes32(BigInt(value));
  }
  throw new Error(`expected integer value, got ${String(value)}`);
}

/**
 * 单个成员的 32 字节编码。
 * 动态类型（bytes/string/变长数组/结构）取其哈希；数组为元素编码拼接后的哈希。
 */
function encodeMember(
  keccak256: Keccak256Fn,
  types: TypedDataTypes,
  type: string,
  value: TypedDataValue,
): Uint8Array {
  const arrayBase = arrayBaseType(type);
  if (arrayBase !== null) {
    if (!Array.isArray(value)) {
      throw new Error(`expected array for type ${type}`);
    }
    const fixedLength = fixedArrayLength(type);
    if (fixedLength !== null && value.length !== fixedLength) {
      throw new Error(
        `fixed array ${type} expects ${fixedLength} elements, got ${value.length}`,
      );
    }
    const encodedElements = concatBytes(
      value.map((element) =>
        encodeMember(keccak256, types, arrayBase, element),
      ),
    );
    return keccak256(encodedElements);
  }

  if (type === STRING) {
    if (typeof value !== "string") {
      throw new Error("string type expects a string value");
    }
    return keccak256(utf8ToBytes(value));
  }

  if (type === DYNAMIC_BYTES) {
    if (typeof value !== "string") {
      throw new Error("bytes type expects a hex string");
    }
    return keccak256(hexToBytes(value));
  }

  if (isFixedBytes(type)) {
    return encodeFixedBytes(type, value);
  }

  if (type === ADDRESS) {
    if (typeof value !== "string") {
      throw new Error("address type expects a hex string");
    }
    return encodeAddress(value);
  }

  if (type === BOOL) {
    if (typeof value !== "boolean") {
      throw new Error("bool type expects a boolean value");
    }
    return encodeBoolean(value);
  }

  if (isInteger(type)) {
    return encodeInteger(value);
  }

  // 嵌套结构：取其 hashStruct。
  if (types[type]) {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
      throw new Error(`struct type ${type} expects an object value`);
    }
    return hashStruct(keccak256, types, type, value as Record<string, TypedDataValue>);
  }

  throw new Error(`unsupported EIP-712 type: ${type}`);
}

/**
 * encodeData：typeHash 后拼接每个成员的 32 字节编码，成员顺序取声明顺序。
 */
export function encodeData(
  keccak256: Keccak256Fn,
  types: TypedDataTypes,
  primaryType: string,
  value: Record<string, TypedDataValue>,
): Uint8Array {
  const fields = types[primaryType];
  if (!fields) {
    throw new Error(`unknown struct type: ${primaryType}`);
  }
  const encoded = fields.map((field) =>
    encodeMember(keccak256, types, field.type, value[field.name]),
  );
  return concatBytes([typeHash(keccak256, types, primaryType), ...encoded]);
}

/** hashStruct = keccak256(encodeData(primaryType, value))。 */
export function hashStruct(
  keccak256: Keccak256Fn,
  types: TypedDataTypes,
  primaryType: string,
  value: Record<string, TypedDataValue>,
): Uint8Array {
  return keccak256(encodeData(keccak256, types, primaryType, value));
}
