/**
 * 领域模型：组织树、用户、授权、例外与快照。
 *
 * 关键设计：组织节点以稳定 id 标识，name 只是展示标签。
 * 因此“重命名组织”不会改变任何 id 引用，天然不会被算作权限变化。
 */

export interface OrgNode {
  id: string;
  name: string;
  parentId: string | null;
}

export interface OrgTree {
  rootId: string;
  nodes: ReadonlyMap<string, OrgNode>;
}

export interface User {
  id: string;
  orgId: string;
}

/** 挂载在组织节点上的授权，沿组织树向下继承。path 为前缀，覆盖自身及子树。 */
export interface Grant {
  id: string;
  orgId: string;
  resourceType: string;
  path: string;
  actions: string[];
}

export type ExceptionEffect = 'allow' | 'deny';

/** 针对单个用户的显式例外，优先级高于继承来的授权。 */
export interface UserException {
  id: string;
  userId: string;
  resourceType: string;
  path: string;
  effect: ExceptionEffect;
  actions: string[];
}

/** 资源目录：每类资源的全量具体路径（用于前缀展开与压缩）。 */
export type ResourceCatalog = Readonly<Record<string, readonly string[]>>;

/** 一次迁移前/后的完整快照。 */
export interface Snapshot {
  tree: OrgTree;
  users: readonly User[];
  grants: readonly Grant[];
  exceptions: readonly UserException[];
  catalog: ResourceCatalog;
}

/** 来源链上的一环，从用户出发指向权限来源。 */
export interface ChainLink {
  kind: 'user' | 'org' | 'grant' | 'exception';
  id: string;
  label: string;
}

export type SourceChain = ChainLink[];

/** 一条（压缩后的）资源路径及其最短来源链。 */
export interface SourcedPath {
  path: string;
  chain: SourceChain;
}

export interface ActionDiff {
  action: string;
  added: SourcedPath[];
  lost: SourcedPath[];
  unchanged: SourcedPath[];
}

export interface UserResourceDiff {
  userId: string;
  resourceType: string;
  actions: ActionDiff[];
  hasChanges: boolean;
}

export interface DiffReport {
  users: UserResourceDiff[];
}
