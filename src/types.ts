/**
 * 锁定文件闭合扫描的公共类型定义。
 */

/** package-lock.json 中 lockfileVersion 3 的单个 packages 条目（仅声明需要的字段）。 */
export interface LockfilePackageEntry {
  version?: string;
  resolved?: string;
  /** 别名安装时，该字段保存包的真实名称。 */
  name?: string;
  link?: boolean;
  dev?: boolean;
  optional?: boolean;
  peer?: boolean;
  [key: string]: unknown;
}

/** package-lock.json 的最小结构。 */
export interface PackageLock {
  name?: string;
  version?: string;
  lockfileVersion: number;
  packages?: Record<string, LockfilePackageEntry>;
  [key: string]: unknown;
}

/** 从锁定文件中解析出的、需要纳入评估的真实包。 */
export interface LockedPackage {
  /** 真实包名：name 字段优先，否则取路径中最后一个 node_modules 段。 */
  realName: string;
  /** 锁定版本（已去除构建元数据参与比较时的原始值仍保留）。 */
  version: string;
  /** packages 字典中的键（安装路径）。 */
  installPath: string;
  /** 条目中声明的 name 字段（别名场景下与路径末段不同）。 */
  declaredName?: string;
  /** 条目的 resolved 值（可能缺失）。 */
  resolved?: string;
  /** 别名安装：声明名与真实名不一致。 */
  aliased: boolean;
}

/** resolved 主机不在允许表（或 URL 无法解析）时的报告。 */
export interface ResolvedHostViolation {
  kind: 'resolved-host';
  installPath: string;
  realName: string;
  version: string;
  resolved?: string;
  /** 违规原因：disallowed-host | missing-resolved | unparsable-resolved。 */
  reason: 'disallowed-host' | 'missing-resolved' | 'unparsable-resolved';
  host?: string;
  allowedHosts: string[];
}

/** OSV SEMVER 区间中的单个事件。 */
export interface OsvSemverEvent {
  introduced?: string;
  fixed?: string;
  last_affected?: string;
}

/** OSV affected 条目中的一个版本范围（仅支持 ECOSYSTEM/SEMVER）。 */
export interface OsvAffectedRange {
  type?: string;
  events: OsvSemverEvent[];
}

/** 经过归一化的 OSV 公告（按真实包名匹配）。 */
export interface OsvAdvisory {
  id: string;
  packageName: string;
  ranges: OsvAffectedRange[];
}

/** 单个锁定包的评估结果。 */
export interface PackageEvaluation {
  realName: string;
  version: string;
  installPath: string;
  /** 命中的公告 id 列表；为空表示未受已知公告影响。 */
  affectedBy: string[];
  /** 是否确实拿到了可评估的 OSV 数据并完成判定。 */
  evaluated: boolean;
  /** evaluated 为 false 时的原因。 */
  reason?:
    | 'no-osv-data'
    | 'unsupported-range-type'
    | 'invalid-version'
    | 'invalid-advisory-event';
}

/** 发布门禁消费的闭合扫描结果。 */
export interface LockfileClosureResult {
  /** 扫描的锁定文件版本。 */
  lockfileVersion: number;
  /** 解析出的全部待评估包。 */
  packages: LockedPackage[];
  /** 每个锁定包的 OSV 评估结果（与 packages 一一对应）。 */
  evaluations: PackageEvaluation[];
  /** resolved 主机违规报告。 */
  hostViolations: ResolvedHostViolation[];
  /**
   * 闭合：锁定文件可解析，且每个被锁定的包版本都拿到了评估结果。
   * 闭合不等于无漏洞，仅表示评估覆盖完整、数据链路闭合。
   */
  closed: boolean;
  /** 门禁通过：闭合、无主机违规、无受影响包。 */
  passed: boolean;
  /** 未闭合或失败时的说明。 */
  errors: string[];
}

/** 扫描入参。 */
export interface ScanOptions {
  /** resolved URL 允许的主机（host，不含端口/协议），默认仅官方 registry。 */
  allowedHosts?: string[];
  /**
   * 按真实包名提供的 OSV 公告。缺失的包视为“未评估”，整体不闭合。
   * 值为空数组表示该包已查询且无公告（评估完成、不受影响）。
   */
  advisories: Record<string, OsvAdvisory[]>;
}
