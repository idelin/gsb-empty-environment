export interface NodeSpec {
  id: string;
  parent: string | null;
  fixed?: boolean;
  grants?: string[];
  refs?: string[];
  targetParent?: string;
}

export interface Scenario {
  name?: string;
  nodes: NodeSpec[];
}

export interface TreeNode {
  id: string;
  parentId: string | null;
  children: string[];
  grants: string[];
  refs: string[];
  fixed: boolean;
  targetParent?: string;
}

export interface Tree {
  rootId: string;
  nodes: Map<string, TreeNode>;
}

export type MoveReason = 'target' | 'cycle-break';

export interface Move {
  id: string;
  from: string | null;
  to: string;
  reason: MoveReason;
}

export interface ExceptionEntry {
  node: string;
  permission: string;
  kind: 'grant' | 'deny';
}

export type RefStatus = 'unchanged' | 'redirected' | 'broken' | 'fixed' | 'broken-both';

export interface RefResolution {
  owner: string;
  ref: string;
  beforeId: string | null;
  beforePath: string | null;
  afterId: string | null;
  afterPath: string | null;
  status: RefStatus;
}

export interface Plan {
  scenario: string;
  feasible: boolean;
  problems: string[];
  moves: Move[];
  moveCount: number;
  exceptions: ExceptionEntry[];
  exceptionCount: number;
  refResolutions: RefResolution[];
  identityPreserved: boolean;
}
