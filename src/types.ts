/**
 * iOS 数据保护类（NSFileProtection）推导包的公共类型定义。
 *
 * 保护类强弱依次为（强 -> 弱）：
 *   complete > completeUnlessOpen > completeUntilFirstUserAuthentication > none
 */

/** iOS 数据保护类。 */
export type ProtectionClass =
  | 'complete'
  | 'completeUnlessOpen'
  | 'completeUntilFirstUserAuthentication'
  | 'none';

/** 对单个路径依次执行的操作类型。 */
export type OperationType =
  | 'create'
  | 'atomicWrite'
  | 'copy'
  | 'move'
  | 'setAttributes'
  | 'restore';

/** 操作的公共字段；`id` 可选，用于和静态审查侧的源码位置关联。 */
export interface BaseOperation {
  /** 操作类型（判别字段）。 */
  op: OperationType;
  /** 目标路径。 */
  path: string;
  /** 调用方可选的稳定标识，便于回指源码调用点。 */
  id?: string;
  /**
   * create / atomicWrite / setAttributes 显式指定的保护类。
   * create、atomicWrite 缺省时取应用默认类；setAttributes 必须显式给出。
   */
  protectionClass?: ProtectionClass;
}

/** create：指定类或缺省（缺省取应用默认类）。 */
export interface CreateOperation extends BaseOperation {
  op: 'create';
}

/**
 * atomicWrite：指定类或缺省（缺省取应用默认类）。
 * 原子写会以新类整体替换文件，不继承被替换文件的原类。
 */
export interface AtomicWriteOperation extends BaseOperation {
  op: 'atomicWrite';
}

/** copy：源路径通过 `from` 给出，副本取应用默认类。 */
export interface CopyOperation extends BaseOperation {
  op: 'copy';
  from: string;
}

/** move：源路径通过 `from` 给出，目标保留源文件的原类。 */
export interface MoveOperation extends BaseOperation {
  op: 'move';
  from: string;
}

/** setAttributes：必须显式给出保护类。 */
export interface SetAttributesOperation extends BaseOperation {
  op: 'setAttributes';
  protectionClass: ProtectionClass;
}

/** restore：备份恢复，一律取应用默认类。 */
export interface RestoreOperation extends BaseOperation {
  op: 'restore';
}

/** 受判别联合约束的操作序列元素。 */
export type Operation =
  | CreateOperation
  | AtomicWriteOperation
  | CopyOperation
  | MoveOperation
  | SetAttributesOperation
  | RestoreOperation;

/**
 * 路径规则：前缀 `prefix` 下的所有路径，其保护类不得弱于 `minClass`。
 * 一条路径命中多条规则时，取其中最强的要求。
 */
export interface PathRule {
  /** 路径前缀（目录或具体路径）。 */
  prefix: string;
  /** 该前缀要求的最低保护类。 */
  minClass: ProtectionClass;
}

/** 推导输入。 */
export interface InferenceInput {
  /** 对各路径依次执行的操作序列。 */
  operations: Operation[];
  /** 调用方给定的路径规则。 */
  rules: PathRule[];
  /** 调用方给定的应用默认保护类。 */
  defaultClass: ProtectionClass;
}

/** 单个路径的最终推导结果。 */
export interface PathResult {
  path: string;
  /** 按序推导得到的最终保护类。 */
  finalClass: ProtectionClass;
  /** 最后一次决定该路径类的操作下标（对应输入 operations）。 */
  decidingOperationIndex: number;
  /** 最后一次决定该路径类的操作 id（若提供）。 */
  decidingOperationId?: string;
  /** 命中的规则中最强的要求类；无规则命中时为 undefined。 */
  requiredClass?: ProtectionClass;
  /** 最终类是否满足所有命中规则。 */
  compliant: boolean;
}

/** 造成降级、触发违规的那一步及其溯源信息。 */
export interface Violation {
  path: string;
  requiredClass: ProtectionClass;
  actualClass: ProtectionClass;
  /** 造成最终弱类的操作下标。 */
  violatingOperationIndex: number;
  /** 造成最终弱类的操作类型。 */
  violatingOperation: OperationType;
  /** 造成最终弱类的操作 id（若提供）。 */
  violatingOperationId?: string;
  /**
   * 当弱类由 move 带入规则目录时，给出源路径上“最初设类”的操作下标。
   * 非 move 场景为 undefined。
   */
  originOperationIndex?: number;
  originOperationId?: string;
  moveSourcePath?: string;
  /** 面向审查报告的中文说明。 */
  reason: string;
}

/** 推导输出。 */
export interface InferenceResult {
  /** 每个仍存在路径的最终类与合规情况，按最后作用操作的先后排序。 */
  paths: PathResult[];
  /** 最终类弱于规则要求的违规列表。 */
  violations: Violation[];
}

/** 输入不合法时抛出，携带出错操作下标，便于定位。 */
export class InferenceError extends Error {
  readonly operationIndex: number;
  readonly operationType?: OperationType;

  constructor(
    message: string,
    operationIndex: number,
    operationType?: OperationType,
  ) {
    super(message);
    this.name = 'InferenceError';
    this.operationIndex = operationIndex;
    this.operationType = operationType;
  }
}
