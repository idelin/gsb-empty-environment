/**
 * 求解器的公共数据模型。
 *
 * 语义约定：
 * - 版本号是非负整数；
 * - 版本区间均为闭区间，缺省边界表示无限制；
 * - “升级数”指最终方案中与 `installed` 当前版本不一致（含新装）的包数量；
 * - 可选特性（feature）由根需求声明启用，用于门控对应的依赖边。
 */

/** 闭区间版本范围，min/max 缺省表示该方向无界。 */
export interface Range {
  min?: number;
  max?: number;
}

/** 一条依赖边。 */
export interface DepSpec {
  /** 依赖目标包名。 */
  pkg: string;
  /** 目标包允许的版本区间，缺省表示任意版本。 */
  range?: Range;
  /** 仅当所属包启用了该特性时，此依赖才生效。 */
  feature?: string;
  /**
   * 软依赖：不会主动把目标包拉入方案；
   * 但若目标包因其他原因被安装，则必须满足该区间。
   */
  optional?: boolean;
}

/** 一条冲突边：本版本不允许与目标包的区间内版本共存。 */
export interface ConflictSpec {
  pkg: string;
  /** 缺省表示与目标包的所有版本冲突。 */
  range?: Range;
}

/** 某个包的一个可用版本及其约束。 */
export interface PackageVersion {
  version: number;
  requires?: DepSpec[];
  conflicts?: ConflictSpec[];
}

/** 仓库中一个包的全部可用版本。 */
export interface PackageDef {
  name: string;
  versions: PackageVersion[];
}

/** 根需求：用户直接要求安装的包。 */
export interface RootRequirement {
  pkg: string;
  range?: Range;
  /** 为该包启用的可选特性。 */
  features?: string[];
}

/** 求解输入。 */
export interface SolveInput {
  repository: PackageDef[];
  required: RootRequirement[];
  /** 当前已安装的包版本；用于计算“升级数”。 */
  installed?: Record<string, number>;
}

/** 安装方案：包名 -> 选定版本。 */
export interface InstallPlan {
  [pkg: string]: number;
}
