/**
 * 隔离判定包输入输出类型。所有判定函数均无副作用，可在每次调谐时直接调用。
 */

export type CreationPolicy = 'Owner' | 'Orphan' | 'Merge' | 'None';

export type StoreKind = 'SecretStore' | 'ClusterSecretStore';

export type StoreScope = 'namespace' | 'cluster';

export interface LabelSelector {
  matchLabels?: Record<string, string>;
  matchExpressions?: LabelSelectorRequirement[];
}

export interface LabelSelectorRequirement {
  key: string;
  operator: 'In' | 'NotIn' | 'Exists' | 'DoesNotExist';
  values?: string[];
}

export interface DataFromEntry {
  /** 直接读取的具体 key（与 find 互斥；二者可同时出现于不同条目）。 */
  key?: string;
  /** 按正则在密钥库中批量发现 key（ESO find.name 语义：子串匹配，非整串）。 */
  find?: { name: string };
}

export interface ExternalSecretInput {
  /** ExternalSecret 所在命名空间，即调谐发起方。 */
  namespace: string;
  /** 存储引用种类。 */
  storeRefKind: StoreKind;
  /** 存储引用名称。 */
  storeRefName: string;
  dataFrom: DataFromEntry[];
  /** 目标 Secret 名称（同命名空间，命名空间取 ExternalSecret.namespace）。 */
  targetSecretName: string;
  creationPolicy: CreationPolicy;
}

export interface StoreDefinition {
  kind: StoreKind;
  name: string;
  scope: StoreScope;
  /**
   * 存储所在命名空间。命名空间范围的 SecretStore 必须填写；
   * 集群范围的 ClusterSecretStore 忽略该字段。
   */
  namespace?: string;
  /** 集群范围存储允许服务的命名空间白名单，为空表示不允许任何命名空间。 */
  allowedNamespaces?: string[];
  /** 集群范围存储额外的标签选择器条件，与白名单为 AND 关系。 */
  namespaceSelector?: LabelSelector;
  /** 允许读取的 key 前缀列表，所有 key 必须命中其中之一。 */
  allowedKeyPrefixes: string[];
}

export interface OwnerReference {
  apiVersion: string;
  kind: string;
  name: string;
  uid: string;
}

export interface ExistingTargetSecret {
  name: string;
  namespace: string;
  /** 当前属主引用，空数组表示无属主。 */
  ownerReferences: OwnerReference[];
  /** 当前 Secret 中的数据键。 */
  data: Record<string, string>;
  /**
   * Secret 标签。受保护键通过两种形式声明：
   * 1) "eso.example.com/protected-keys": "a,b,c"（逗号分隔，受字符集限制）
   * 2) "eso.example.com/protected-key/<键名>": "true"
   */
  labels?: Record<string, string>;
}

export interface EsoIdentity {
  apiVersion: string;
  kind: string;
  name: string;
  uid: string;
}

export interface IsolationEvaluation {
  externalSecret: ExternalSecretInput;
  /** 解析到的存储定义；存储不存在时传 undefined。 */
  store: StoreDefinition | undefined;
  /** 命名空间标签，用于集群存储的标签选择器判定。 */
  namespaceLabels?: Record<string, string>;
  existingTarget?: ExistingTargetSecret | undefined;
  /** 本 ExternalSecret 自身的控制器身份，用于 Owner 属主比对。 */
  self: EsoIdentity;
  /**
   * 密钥库中当前实际存在的键。提供时对 find.name 做权威的运行时判定；
   * 不提供时仅做静态保守判定（探针 + 锚点要求）。
   */
  providerKeys?: string[];
  /** 本次刷新结果。 */
  refresh: RefreshResult;
}

export interface RefreshResult {
  /** 外部密钥库调用是否失败（网络、鉴权、限流等）。 */
  failed: boolean;
  /**
   * 刷新取回的数据。failed 为 true 时忽略；
   * 值为空字符串表示该键本次未取到有效内容。
   */
  fetched: Record<string, string>;
}

export type FindingCode =
  | 'STORE_REF_KIND_UNSUPPORTED'
  | 'STORE_NOT_FOUND'
  | 'CROSS_NAMESPACE_STORE_REF'
  | 'NAMESPACE_NOT_WHITELISTED'
  | 'NAMESPACE_LABELS_REJECTED'
  | 'NO_ALLOWED_KEY_PREFIX'
  | 'KEY_PREFIX_VIOLATION'
  | 'FIND_REGEX_INVALID'
  | 'FIND_REGEX_PREFIX_ESCAPE'
  | 'FIND_MATCHED_DISALLOWED_KEY'
  | 'FOREIGN_OWNER'
  | 'PROTECTED_KEY_OVERWRITE'
  | 'REFRESH_FAILED'
  | 'EMPTY_VALUE_DOWNGRADE'
  | 'TARGET_MISSING_WITH_NONE_POLICY';

export interface Finding {
  code: FindingCode;
  message: string;
  details?: Record<string, string | string[] | boolean>;
}

export type DecisionAction = 'apply' | 'deny' | 'skip';

export interface IsolationDecision {
  action: DecisionAction;
  allowed: boolean;
  /** 拒绝原因（安全/配置违例），出现即 deny。 */
  denials: Finding[];
  /** 非拒绝性提示，如刷新失败跳过、空值降级。 */
  notices: Finding[];
  /** 通过全部检查后规划出的实际写入数据；deny/skip 时为空对象。 */
  plannedData: Record<string, string>;
}

/** 出现即必须拒绝的判定码。 */
export const DENIAL_CODES: ReadonlySet<FindingCode> = new Set<FindingCode>([
  'STORE_REF_KIND_UNSUPPORTED',
  'STORE_NOT_FOUND',
  'CROSS_NAMESPACE_STORE_REF',
  'NAMESPACE_NOT_WHITELISTED',
  'NAMESPACE_LABELS_REJECTED',
  'NO_ALLOWED_KEY_PREFIX',
  'KEY_PREFIX_VIOLATION',
  'FIND_REGEX_INVALID',
  'FIND_REGEX_PREFIX_ESCAPE',
  'FIND_MATCHED_DISALLOWED_KEY',
  'FOREIGN_OWNER',
  'PROTECTED_KEY_OVERWRITE',
]);
