/**
 * 对象图描述类型。
 *
 * 图是纯数据：对象之间的关联全部用 `{ $ref }` 指针表达，
 * 这样内容域的代码永远不会因为特权侧读取图而被执行。
 */

export type Domain = string;

export interface Ref {
  readonly $ref: string;
}

export type Primitive = string | number | bigint | boolean | null | undefined;

export type SlotValue = Ref | Primitive | readonly SlotValue[];

export type XrayCallImpl = (
  thisRef: unknown,
  args: readonly unknown[],
  calleeId: string,
) => unknown;

export interface DataProperty {
  readonly kind: 'data';
  readonly value: SlotValue;
  readonly enumerable?: boolean;
  readonly writable?: boolean;
  readonly configurable?: boolean;
}

export interface AccessorProperty {
  readonly kind: 'accessor';
  readonly enumerable?: boolean;
  readonly configurable?: boolean;
}

/** 原生属性：内容域无法替换其语义，访问器只登记存在，没有可执行函数。 */
export type NativeProperty = DataProperty | AccessorProperty;

export interface ContentAccessorProperty extends AccessorProperty {
  /** 仅供内容域模拟侧调用；Xray 视图永远不会触碰这两个函数。 */
  readonly get?: () => unknown;
  readonly set?: (value: unknown) => void;
}

/** 内容域追加或遮蔽的属性，可能携带内容侧函数。 */
export type ContentProperty = DataProperty | ContentAccessorProperty;

export interface GraphNode {
  readonly id: string;
  readonly domain: Domain;
  /** 内置类型标签，例如 Object / Array / Function / ObjectPrototype。 */
  readonly intrinsic: string;
  /** 该对象类型的原生自有属性表。 */
  readonly nativeProps?: Record<string, NativeProperty>;
  /** 内容域追加或遮蔽的属性（遮蔽键即使与原生同名也不出现在视图里）。 */
  readonly contentProps?: Record<string, ContentProperty>;
  /** 原生原型指针，视图的原型链只走这里。 */
  readonly nativeProto?: string | null;
  /** 内容域替换后的原型指针，仅内容域模拟侧使用。 */
  readonly contentProto?: string | null;
  /** 类数组对象的下标槽位（例如 Array 实例）。 */
  readonly arraySlots?: readonly SlotValue[];
  /** 原生函数的特权侧实现，视图被调用时执行它而不是任何内容函数。 */
  readonly privilegedCall?: XrayCallImpl;
}

export interface ObjectGraph {
  readonly nodes: Record<string, GraphNode>;
}

export const ref = (id: string): Ref => ({ $ref: id });

export const isRef = (value: unknown): value is Ref =>
  typeof value === 'object' && value !== null &&
  Object.prototype.hasOwnProperty.call(value, '$ref');

export const nativeData = (
  value: SlotValue,
  flags: { enumerable?: boolean; writable?: boolean; configurable?: boolean } = {},
): DataProperty => ({ kind: 'data', value, ...flags });

export const nativeAccessor = (
  flags: { enumerable?: boolean; configurable?: boolean } = {},
): AccessorProperty => ({ kind: 'accessor', ...flags });

export const contentData = (
  value: SlotValue,
  flags: { enumerable?: boolean; writable?: boolean; configurable?: boolean } = {},
): DataProperty => ({ kind: 'data', value, ...flags });

export const contentAccessor = (
  accessor: { get?: () => unknown; set?: (value: unknown) => void; enumerable?: boolean; configurable?: boolean } = {},
): ContentAccessorProperty => ({ kind: 'accessor', ...accessor });
