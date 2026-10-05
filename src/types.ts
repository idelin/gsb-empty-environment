export interface NodeSpec {
  id: string;
  name: string;
  parent: string | null;
  pinned?: boolean;
  acl?: string[];
  refs?: string[];
}

export interface TargetGrouping {
  mustBeUnder: Record<string, string>;
}

export interface Move {
  nodeId: string;
  newParentId: string;
}

export interface TreeState {
  root: string;
  parent: Map<string, string | null>;
  name: Map<string, string>;
  pinned: Set<string>;
  localAcl: Map<string, readonly string[]>;
  refs: Map<string, readonly string[]>;
}

export interface Violation {
  node: string;
  container: string;
}

export interface PermissionException {
  nodeId: string;
  gained: string[];
  lost: string[];
}

export type RefStatus =
  | "UNCHANGED"
  | "RETARGETED"
  | "BROKEN"
  | "REPAIRED"
  | "STILL_BROKEN";

export interface RefResolution {
  nodeId: string;
  path: string;
  before: string | null;
  after: string | null;
  status: RefStatus;
}

export interface PlanResult {
  moves: Move[];
  finalState: TreeState;
  exceptions: PermissionException[];
  exceptionNodeCount: number;
  exceptionEntryCount: number;
  refResolutions: RefResolution[];
  exploredStates: number;
}
