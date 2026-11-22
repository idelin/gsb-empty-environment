/** 逐字段 / 逐节点的披露声明。 */
export type Disclosure =
  | { kind: 'public' }
  | { kind: 'tenant' }
  | { kind: 'restricted'; level: number };

/** 解码后的字段值。 */
export type FieldValue =
  | string
  | number
  | boolean
  | null
  | Uint8Array
  | FieldValue[]
  | { [key: string]: FieldValue };

/**
 * 错误原因图节点。
 * causes / details 是有序边；同一对象可被多条边引用（共享），也可成环。
 */
export interface ErrorNode {
  service: string;
  tenant: string;
  typeUrl: string;
  raw: Uint8Array;
  /** 节点级披露声明：决定该节点对调用者是否可见。 */
  visibility: Disclosure;
  /** 逐字段披露声明；未声明的字段一律不披露（fail-closed）。 */
  disclose: Readonly<Record<string, Disclosure>>;
  causes: ErrorNode[];
  details: ErrorNode[];
}

/** 调用者上下文：租户、权限级别与权限代次。 */
export interface ViewerContext {
  tenant: string;
  clearance: number;
  generation: number;
}
