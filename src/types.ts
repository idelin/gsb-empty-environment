export interface Heading {
  id: string;
  title: string;
  level: number;
}

export interface FixRange {
  startId: string;
  endId: string;
  headingIds: string[];
}

export interface LevelViolation {
  headingId: string;
  title: string;
  actualLevel: number;
  maxAllowedLevel: number;
  fixRange: FixRange;
  suggestion: { kind: 'shift-subtree'; delta: number };
  alternative: { kind: 'insert-ancestors'; count: number };
}

export interface NumberChange {
  id: string;
  title: string;
  oldNumber: string | null;
  newNumber: string | null;
  kind: 'added' | 'removed' | 'changed';
}

export interface Reference {
  id: string;
  targetId: string;
  pinnedVersion?: number;
}

export interface VersionedHeadingView {
  version: number;
  number: string | null;
  title: string;
}

export type ResolvedReference =
  | {
      status: 'ok';
      referenceId: string;
      targetId: string;
      number: string | null;
      title: string;
      pinned?: VersionedHeadingView;
      changedSincePinned: boolean;
    }
  | {
      status: 'dangling';
      referenceId: string;
      targetId: string;
      lastKnown: VersionedHeadingView | null;
    };

export interface DanglingReferenceInfo {
  referenceId: string;
  targetId: string;
  lastKnown: VersionedHeadingView | null;
}

export interface EditResult {
  version: number;
  changes: NumberChange[];
  violations: LevelViolation[];
  danglingReferences: DanglingReferenceInfo[];
}
