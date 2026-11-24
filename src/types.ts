/**
 * Public types for the removable-storage protection verifier.
 *
 * The model deliberately keeps three identities separate:
 *   - a path name (a fixed string token),
 *   - an opened handle (a fixed string token),
 *   - a file object (an inode-like identity created by the host).
 *
 * Renaming, unlinking or restoring a path therefore never retargets a handle.
 */

export type ProtectionClass = "public" | "untilLock" | "complete";

export const PROTECTION_CLASSES: readonly ProtectionClass[] = [
  "public",
  "untilLock",
  "complete",
];

export function isProtectionClass(value: unknown): value is ProtectionClass {
  return (
    value === "public" || value === "untilLock" || value === "complete"
  );
}

/** Larger means more restrictive. */
export function classRank(value: ProtectionClass): number {
  switch (value) {
    case "public":
      return 0;
    case "untilLock":
      return 1;
    case "complete":
      return 2;
  }
  throw new Error(`unknown protection class ${String(value)}`);
}

export function stricterClass(
  left: ProtectionClass,
  right: ProtectionClass,
): ProtectionClass {
  return classRank(left) >= classRank(right) ? left : right;
}

export type EventId = string;
export type PathId = string;
export type HandleId = string;
export type VolumeId = string;
export type ObjectId = string;

export type Operation =
  /**
   * Create a brand-new object at an absent path.  The path must not exist.
   */
  | {
      kind: "create";
      path: PathId;
      protectionClass?: ProtectionClass;
      contentLabel?: ProtectionClass;
    }
  /**
   * Backup-restore a path as a new object.  An existing path is replaced but
   * old handles continue to refer to the old object.
   */
  | {
      kind: "restore";
      path: PathId;
      protectionClass?: ProtectionClass;
      contentLabel?: ProtectionClass;
    }
  /**
   * Replace an existing path by a new object.  The path must exist.
   */
  | {
      kind: "replace";
      path: PathId;
      protectionClass?: ProtectionClass;
      contentLabel?: ProtectionClass;
    }
  | { kind: "open"; path: PathId; handle: HandleId }
  | { kind: "close"; handle: HandleId }
  | { kind: "read"; handle: HandleId; path?: PathId }
  | { kind: "readPath"; path: PathId }
  | { kind: "unlink"; path: PathId }
  | { kind: "rename"; from: PathId; to: PathId }
  | { kind: "link"; from: PathId; to: PathId }
  | {
      kind: "setClass";
      class: ProtectionClass;
      path?: PathId;
      handle?: HandleId;
    }
  /**
   * Copy through a source handle.  The destination is a new object whose
   * protection class is the target volume default (unless explicitly set),
   * while the source content label is always retained on the copy.
   */
  | {
      kind: "copy";
      handle: HandleId;
      to: PathId;
      protectionClass?: ProtectionClass;
      contentLabel?: ProtectionClass;
    }
  | { kind: "lock" }
  | { kind: "unlock" };

export interface EventSpec {
  readonly id: EventId;
  readonly op: Operation;
  /** Events that must precede this event. */
  readonly before?: readonly EventId[];
  /** Alias for before, accepted for convenience. */
  readonly deps?: readonly EventId[];
  /** Events that this event must precede. */
  readonly after?: readonly EventId[];
}

export interface PathSpec {
  readonly id: PathId;
  readonly volume?: VolumeId;
}

export interface InitialPathSpec {
  readonly path: PathId;
  /** Protection class of the initial object. */
  readonly protectionClass?: ProtectionClass;
  /** Alias for protectionClass. */
  readonly class?: ProtectionClass;
  /** Confidentiality label of the initial content. Defaults to class. */
  readonly contentLabel?: ProtectionClass;
  readonly volume?: VolumeId;
  /** Optional deterministic object id. */
  readonly objectId?: ObjectId;
}

export type VolumeConfig =
  | ProtectionClass
  | { readonly defaultClass: ProtectionClass };

export type VolumeMap = Readonly<Record<VolumeId, VolumeConfig>>;

export interface OperationGraph {
  readonly events: readonly EventSpec[];
  readonly initial?: readonly InitialPathSpec[];
  readonly paths?: readonly PathSpec[];
  readonly volumes?: VolumeMap;
  readonly pathVolumes?: Readonly<Record<PathId, VolumeId>>;
  readonly resolveVolume?: (path: PathId) => VolumeId;
  readonly defaultVolume?: VolumeId;
  readonly defaultClass?: ProtectionClass;
}

export interface Budget {
  /** Maximum number of distinct reachable states kept per analysis. */
  readonly maxStates?: number;
  /** Maximum number of checked enabled-event transitions. */
  readonly maxTransitions?: number;
  /** Maximum prefix length explored. */
  readonly maxDepth?: number;
}

export interface AnalyzeOptions extends Budget {
  /** Optional nested form of the budget options. */
  readonly budget?: Budget;
  readonly volumes?: VolumeMap;
  readonly pathVolumes?: Readonly<Record<PathId, VolumeId>>;
  readonly resolveVolume?: (path: PathId) => VolumeId;
  readonly defaultVolume?: VolumeId;
  readonly defaultClass?: ProtectionClass;
  /** Hard limit on graph size.  The specification fixes it at 24. */
  readonly maxEvents?: number;
  /** Locale-independent event id comparison, used for the lexicographic tie. */
  readonly compareEventIds?: (left: EventId, right: EventId) => number;
}

export interface Rejection {
  readonly code: string;
  readonly message: string;
  readonly event: EventId;
}

export interface ViolationDetail {
  readonly event: EventId;
  readonly kind: "read" | "readPath" | "copy";
  readonly objectId: ObjectId;
  readonly handle?: HandleId;
  readonly path?: PathId;
  readonly protectionClass: ProtectionClass;
  readonly contentLabel: ProtectionClass;
  readonly lockSerial: number;
}

export interface StepRecord {
  readonly event: EventId;
  readonly outcome: "applied" | "rejected";
  readonly rejection?: Rejection;
  readonly violation?: ViolationDetail;
}

export interface ViolationWitness {
  readonly prefix: readonly EventId[];
  readonly violation: ViolationDetail;
  readonly steps: readonly StepRecord[];
}

export interface UnsafeResult {
  readonly status: "unsafe";
  readonly prefix: readonly EventId[];
  readonly violation: ViolationDetail;
  readonly witness: ViolationWitness;
  readonly explored: ExplorationStats;
}

export interface SafeResult {
  readonly status: "safe";
  readonly proof: SafetyProof;
  readonly explored: ExplorationStats;
}

export interface UnknownResult {
  readonly status: "unknown";
  readonly reason: "budget";
  readonly budget: Required<Budget>;
  readonly explored: ExplorationStats;
  readonly partial?: {
    readonly violation?: ViolationWitness;
  };
}

export interface InvalidResult {
  readonly status: "invalid";
  readonly reason: string;
}

export type AnalysisResult =
  | UnsafeResult
  | SafeResult
  | UnknownResult
  | InvalidResult;

export interface ExplorationStats {
  readonly components: number;
  readonly states: number;
  readonly transitions: number;
  readonly enabledOccurrences: number;
  readonly events: number;
}

export interface SerializedObject {
  readonly id: ObjectId;
  readonly protectionClass: ProtectionClass;
  readonly contentLabel: ProtectionClass;
  readonly volume: VolumeId;
}

export interface SerializedHandle {
  readonly id: HandleId;
  readonly objectId: ObjectId;
  readonly openedAtLockSerial: number;
  readonly valid: boolean;
}

/**
 * A fully serialized machine state.  `executed` is a set, serialized sorted.
 * State ids are local to a component proof.
 */
export interface SerializedState {
  readonly id: number;
  readonly objects: readonly SerializedObject[];
  readonly paths: readonly (readonly [PathId, ObjectId])[];
  readonly handles: readonly SerializedHandle[];
  readonly locked: boolean;
  readonly lockSerial: number;
  readonly executed: readonly EventId[];
}

export interface ClosureTransition {
  readonly from: number;
  readonly event: EventId;
  readonly to: number;
  readonly outcome: "applied" | "rejected";
  readonly rejection?: string;
}

export interface ComponentSafetyProof {
  readonly componentId: number;
  readonly events: readonly EventId[];
  readonly initialState: SerializedState;
  readonly states: readonly SerializedState[];
  readonly transitions: readonly ClosureTransition[];
  readonly checkedEvents: readonly EventId[];
  readonly enabledOccurrences: number;
}

/**
 * A state-closure certificate.  It is intentionally data-only so it can be
 * stored, shipped to another process, and verified without trusting the
 * producer.  The checker replays every transition and also fails when an
 * enabled event has no outgoing edge.
 */
export interface SafetyProof {
  readonly kind: "state-closure";
  readonly version: 1;
  readonly graphDigest: string;
  readonly eventPartition: readonly (readonly EventId[])[];
  readonly components: readonly ComponentSafetyProof[];
  readonly totals: {
    readonly components: number;
    readonly states: number;
    readonly transitions: number;
    readonly enabledOccurrences: number;
    readonly events: number;
  };
}

export interface ProofCheckResult {
  readonly ok: boolean;
  readonly errors: readonly string[];
  readonly checked: {
    readonly components: number;
    readonly states: number;
    readonly transitions: number;
    readonly enabledEvents: number;
    readonly events: number;
  };
}

export interface WitnessCheckResult {
  readonly ok: boolean;
  readonly errors: readonly string[];
  readonly steps: readonly StepRecord[];
}

export const DEFAULT_BUDGET: Required<Budget> = {
  maxStates: 200_000,
  maxTransitions: 1_000_000,
  maxDepth: 24,
};

export const MAX_SPEC_EVENTS = 24;
