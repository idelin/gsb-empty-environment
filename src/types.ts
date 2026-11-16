/**
 * 一个已解析的 .npmrc 键值行。
 */
export interface NpmrcEntry {
  /** 1 起始的源文件行号，便于报错定位。 */
  line: number;
  key: string;
  value: string;
  /** 去掉首尾空白后的原始行。 */
  raw: string;
}

export type IssueCode =
  | 'forbidden-executable-key'
  | 'unregistered-auth-host'
  | 'unregistered-registry-host'
  | 'non-whitelisted-env-var'
  | 'invalid-url'
  | 'malformed-line';

export interface NpmrcIssue {
  code: IssueCode;
  line: number;
  key: string;
  message: string;
  /** 结构化细节，方便调用方程序化处理。 */
  detail?: {
    host?: string;
    variable?: string;
  };
}

export interface ValidateOptions {
  /**
   * 调用方登记并信任的 registry 完整 URL 列表，
   * 例如 https://registry.npmjs.org/ 。
   * 认证行与 registry 键的主机都必须落在这个集合内。
   */
  registries: string[];
  /**
   * 允许在 ${VAR} / $VAR 展开中使用的环境变量名
   * （比较时统一转小写）。
   */
  allowedEnvVars: string[];
}

export interface ValidateResult {
  ok: boolean;
  entries: NpmrcEntry[];
  issues: NpmrcIssue[];
}

export interface MergeOptions extends ValidateOptions {
  /** 注入的环境变量，默认读取 process.env。 */
  env?: Record<string, string | undefined>;
}

export interface MergeResult {
  ok: boolean;
  /**
   * 合并后的安全配置。校验失败时只包含 base，
   * 不会混入任何项目文件条目。
   */
  config: Record<string, string>;
  issues: NpmrcIssue[];
}
