// 过程图与分析结果的公共类型定义。
// 仅使用 string-literal 联合类型与普通接口，保证可被 Node 原生 type stripping 加载。

/** 控制流边种类：正常边表示操作成功后的流，异常边表示操作抛出后的流。 */
export type EdgeKind = 'normal' | 'exception';

/** 变量的所有权状态（抽象格用集合表示“可能是”）。 */
export type Status = 'owned' | 'moved' | 'closed';

/**
 * 过程图上的操作。
 * acquire     获取资源（取得所有权）
 * move        移动所有权，原变量失效
 * borrow      借用变量，生成句柄 handle
 * restore     归还借用（按句柄）
 * close       立即关闭
 * deferClose  注册延迟清理：离开 region 时（正常退出或异常展开）按 LIFO 关闭
 * enterRegion / exitRegion  清理区域边界
 * throw       显式抛出；catch 为处理器入口标记
 */
export type Operation =
  | { op: 'entry' }
  | { op: 'noop' }
  | { op: 'acquire'; variable: string }
  | { op: 'move'; from: string; to: string }
  | { op: 'borrow'; variable: string; handle: string }
  | { op: 'restore'; handle: string }
  | { op: 'close'; variable: string }
  | { op: 'deferClose'; variable: string; region: string }
  | { op: 'enterRegion'; region: string }
  | { op: 'exitRegion'; region: string }
  | { op: 'throw' }
  | { op: 'catch' };

export interface CfgNode {
  id: string;
  operation: Operation;
}

export interface CfgEdge {
  from: string;
  to: string;
  kind: EdgeKind;
  /**
 * 异常边展开时依次执行的延迟关闭变量（内层 region 到外层，region 内按 LIFO）。
  * 正常边该字段为空。异常边不应用源节点自身的操作效果（操作已失败）。
  */
  cleanup?: string[];
  /** 构建期使用：异常展开终止于哪个 region（含），缺省为整个活动栈。 */
  unwindThroughRegion?: string;
  /** 分析期使用：本次异常边展开覆盖的 region（内->外），用于注销 pending。 */
  cleanedRegions?: string[];
}

export interface ProcedureGraph {
  nodes: CfgNode[];
  edges: CfgEdge[];
  entry: string;
}

export type ViolationKind =
  | 'useAfterClose' // 对已关闭变量借用/移动/关闭（含把已关闭安全通道传给回调）
  | 'useAfterMove' // 对已移动失效变量继续使用
  | 'useUninitialized' // 使用从未获取成功的变量（部分初始化失败）
  | 'doubleClose' // 重复关闭
  | 'borrowAcrossClose' // 借用仍活跃时变量被关闭（借用越过关闭）
  | 'moveWhileBorrowed' // 变量仍被借用时被移动
  | 'invalidRestore'; // 归还一个不活跃的借用句柄

export interface PathStep {
  kind: 'node' | 'edge';
  node?: string;
  from?: string;
  to?: string;
  edgeKind?: EdgeKind;
  /** 该边上执行的延迟关闭变量（按执行顺序）。 */
  cleanup?: string[];
}

export interface Violation {
  kind: ViolationKind;
  /** 违规定位：节点 id，或异常边上的延迟关闭 `edge:<from>-><to>#<index>`。 */
  location: string;
  variable?: string;
  handle?: string;
  /** 是否由延迟清理触发。 */
  deferred: boolean;
}

export interface ViolationPath {
  violation: Violation;
  /** 从过程入口到违规点的见证路径（含回边，可出现重复节点）。 */
  steps: PathStep[];
}

export interface AnalysisResult {
  violations: ViolationPath[];
  /** 每个节点不动点处的抽象状态摘要。 */
  facts: Record<string, unknown>;
  /** 工作流迭代次数（用于验证循环不动点收敛）。 */
  iterations: number;
}
