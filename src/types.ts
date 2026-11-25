/**
 * 类型图与公共数据结构。
 *
 * 字段身份只由声明编号 `FieldDef.no` 决定；属性名字符串（含别名）仅用于
 * JSON 入口的查表。二进制入口直接使用字段编号，不经过属性名。
 */

export type ScalarKind = 'id64' | 'int32' | 'string' | 'bool';
export type NodeKind = ScalarKind | 'object' | 'array';

export interface FieldDef {
  /** 声明字段编号，全类型图内同对象作用域中唯一的身份。 */
  no: number;
  /**
   * 可接受的 JSON 属性名。第一个是规范名，其余为别名。
   * 同一对象内任何两个字段声明相同名字都会在编译期报错。
   */
  names: string[];
  /** 字段值引用的类型编号。 */
  typeNo: number;
  required?: boolean;
}

export interface ObjectTypeDef {
  kind: 'object';
  no: number;
  name?: string;
  fields: FieldDef[];
}

export interface ArrayTypeDef {
  kind: 'array';
  no: number;
  elementTypeNo: number;
}

export interface ScalarTypeDef {
  kind: ScalarKind;
  no: number;
}

export type TypeDef = ObjectTypeDef | ArrayTypeDef | ScalarTypeDef;

/**
 * 版本迁移：旧版本根对象上的字段 `fromFieldNo` 迁移到当前版本的
 * 嵌套位置 `toPath`（字段编号路径，全部指向对象字段）。
 */
export interface Migration {
  fromVersion: number;
  /**
   * 旧版本根对象属性名（旧 schema 的规范名/别名）-> 当前版本字段编号路径。
   * JSON 入口按属性名识别旧字段；二进制入口按旧字段编号识别，
   * 因此每条迁移同时携带名字与编号两种身份。
   */
  moves: Array<{ fromFieldNo: number; fromName: string; toPath: number[] }>;
}

export interface TypeGraph {
  rootTypeNo: number;
  version: number;
  types: Record<number, TypeDef>;
  migrations: Migration[];
}

export interface DecodeOptions {
  tenant: string;
  /** 为 true 时未知字段直接报错；默认跳过且不进入授权身份。 */
  strictUnknown?: boolean;
  /**
   * 输入消息自报的 schema 版本。缺省使用当前图版本；给出旧版本时
   * 按该版本的迁移规则搬迁字段。
   */
  inputVersion?: number;
}

export type ErrorCode =
  | 'SCHEMA_INVALID'
  | 'SYNTAX_ERROR'
  | 'UNKNOWN_FIELD'
  | 'DUPLICATE_FIELD'
  | 'ALIAS_COLLISION'
  | 'TYPE_MISMATCH'
  | 'ID64_LEXEME'
  | 'RANGE_ERROR'
  | 'LONE_SURROGATE'
  | 'MIGRATION_CONFLICT'
  | 'MIGRATION_UNKNOWN'
  | 'CHECKPOINT_DIGEST'
  | 'CHECKPOINT_STATE'
  | 'TRUNCATED_INPUT'
  | 'UNEXPECTED_END';

export class DecodeError extends Error {
  readonly code: ErrorCode;
  readonly detail?: string;
  constructor(code: ErrorCode, detail?: string) {
    super(detail ? `${code}: ${detail}` : code);
    this.code = code;
    this.detail = detail;
    this.name = 'DecodeError';
  }
}

export interface DecodeResult {
  tenant: string;
  version: number;
  rootTypeNo: number;
  schemaDigest: string;
  /** 按字段编号排序的最小规范编码。 */
  minimal: Uint8Array;
  /** 绑定租户、类型版本与最小编码的签名键（十六进制 SHA-256）。 */
  signatureKey: string;
  identity: import('./identity.ts').IdNode;
}
