/**
 * EIP-712 结构类型定义。键为结构名，值为按声明顺序排列的成员列表。
 * 例如：
 * {
 *   Mail: [
 *     { name: "from", type: "Person" },
 *     { name: "contents", type: "string" },
 *   ],
 *   Person: [
 *     { name: "name", type: "string" },
 *     { name: "wallet", type: "address" },
 *   ],
 * }
 */
export interface TypedDataField {
  name: string;
  type: string;
}

export type TypedDataTypes = Record<string, TypedDataField[]>;

/** 任意可编码的消息值。 */
export type TypedDataValue =
  | string
  | number
  | bigint
  | boolean
  | null
  | undefined
  | TypedDataValue[]
  | { [key: string]: TypedDataValue };

export type TypedDataMessage = Record<string, TypedDataValue>;

/** 调用方注入的 keccak256：输入字节，输出 32 字节哈希。同步签名以简化编码。 */
export type Keccak256Fn = (input: Uint8Array) => Uint8Array;

/**
 * 调用方注入的 ECDSA 地址恢复。
 * @param digest 32 字节消息哈希
 * @param signature 65 字节签名（r || s || v），v 已归一为 0/1
 * @returns 20 字节地址的 0x 十六进制字符串
 */
export type RecoverAddressFn = (
  digest: Uint8Array,
  signature: Uint8Array,
) => string | Promise<string>;
