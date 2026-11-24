import { digestValue } from "./digest.ts";
import {
  MAX_SPEC_EVENTS,
  classRank,
  isProtectionClass,
  type AnalysisResult,
  type AnalyzeOptions,
  type EventId,
  type EventSpec,
  type HandleId,
  type InitialPathSpec,
  type ObjectId,
  type Operation,
  type OperationGraph,
  type PathId,
  type ProtectionClass,
  type Rejection,
  type StepRecord,
  type ViolationDetail,
  type VolumeConfig,
  type VolumeId,
} from "./types.ts";

export class GraphError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "GraphError";
  }
}

export interface NormalizedEvent {
  readonly id: EventId;
  readonly op: Operation;
  /** Direct predecessor ids. */
  readonly before: ReadonlySet<EventId>;
  readonly index: number;
}

export interface ResolvedConfig {
  readonly volumeDefaults: ReadonlyMap<VolumeId, ProtectionClass>;
  readonly pathVolumes: ReadonlyMap<PathId, VolumeId>;
  readonly resolver?: (path: PathId) => VolumeId;
  readonly defaultVolume: VolumeId;
  readonly defaultClass: ProtectionClass;
}

export interface NormalizedGraph {
  readonly events: readonly NormalizedEvent[];
  readonly eventById: ReadonlyMap<EventId, NormalizedEvent>;
  /** Event ids sorted with the configured comparator. */
  readonly order: readonly EventId[];
  readonly initial: readonly InitialPathSpec[];
  readonly config: ResolvedConfig;
  readonly digest: string;
  readonly compareEventIds: (left: EventId, right: EventId) => number;
}

export interface ObjectRecord {
  readonly id: ObjectId;
  readonly protectionClass: ProtectionClass;
  readonly contentLabel: ProtectionClass;
  readonly volume: VolumeId;
}

export interface HandleRecord {
  readonly id: HandleId;
  readonly objectId: ObjectId;
  readonly openedAtLockSerial: number;
  readonly valid: boolean;
}

export interface RuntimeState {
  readonly objects: ReadonlyMap<ObjectId, ObjectRecord>;
  readonly paths: ReadonlyMap<PathId, ObjectId>;
  readonly handles: ReadonlyMap<HandleId, HandleRecord>;
  readonly locked: boolean;
  readonly lockSerial: number;
  readonly executed: ReadonlySet<EventId>;
}

export interface ApplyResult {
  readonly state: RuntimeState;
  readonly outcome: "applied" | "rejected";
  readonly rejection?: Rejection;
  readonly violation?: ViolationDetail;
}

export interface ResolvedVolume {
  readonly id: VolumeId;
  readonly defaultClass: ProtectionClass;
}

function defaultCompare(left: string, right: string): number {
  if (left === right) {
    return 0;
  }
  return left < right ? -1 : 1;
}

function asString(value: unknown, field: string): string {
  if (typeof value === "number" && Number.isFinite(value)) {
    return String(value);
  }
  if (typeof value !== "string" || value.length === 0) {
    throw new GraphError(`${field} must be a non-empty string`);
  }
  return value;
}

function optionalClass(
  value: unknown,
  field: string,
): ProtectionClass | undefined {
  if (value === undefined) {
    return undefined;
  }
  if (!isProtectionClass(value)) {
    throw new GraphError(`${field} is not a protection class`);
  }
  return value;
}

function normalizeVolumeConfig(
  value: VolumeConfig,
  volume: string,
): ProtectionClass {
  if (typeof value === "string") {
    if (!isProtectionClass(value)) {
      throw new GraphError(`volume ${volume} has an invalid class`);
    }
    return value;
  }
  if (value === null || typeof value !== "object") {
    throw new GraphError(`volume ${volume} has an invalid configuration`);
  }
  if (!isProtectionClass(value.defaultClass)) {
    throw new GraphError(`volume ${volume} has an invalid default class`);
  }
  return value.defaultClass;
}

/**
 * Accept the canonical operation shape plus a small set of spelling aliases.
 * Runtime validation is useful because graphs are often produced by parsing
 * JSON rather than by a TypeScript caller.
 */
export function normalizeOperation(raw: unknown, eventId: string): Operation {
  if (raw === null || typeof raw !== "object") {
    throw new GraphError(`event ${eventId} has no operation object`);
  }
  const input = raw as Record<string, unknown>;
  let kind = asString(input.kind, `event ${eventId} operation kind`);
  if (kind === "clone") {
    kind = "copy";
  } else if (kind === "read-path") {
    kind = "readPath";
  } else if (kind === "set-class") {
    kind = "setClass";
  } else if (kind === "hardlink") {
    kind = "link";
  } else if (kind === "delete") {
    kind = "unlink";
  }

  const pathValue = input.path ?? input.target;
  const fromValue =
    input.from ?? input.fromPath ?? input.sourcePath ?? input.source;
  const toValue =
    input.to ?? input.toPath ?? input.destination ?? input.dest ?? input.dst;
  const handleValue =
    input.handle ?? input.handleId ?? input.sourceHandle ?? input.source_handle;

  switch (kind) {
    case "create":
    case "restore":
    case "replace":
      return {
        kind,
        path: asString(pathValue, `event ${eventId} path`),
        protectionClass: optionalClass(
          input.protectionClass ?? input.class,
          `event ${eventId} protectionClass`,
        ),
        contentLabel: optionalClass(
          input.contentLabel ?? input.label,
          `event ${eventId} contentLabel`,
        ),
      };
    case "open":
      return {
        kind,
        path: asString(pathValue, `event ${eventId} path`),
        handle: asString(handleValue, `event ${eventId} handle`),
      };
    case "close":
      return {
        kind,
        handle: asString(handleValue, `event ${eventId} handle`),
      };
    case "read":
      if (handleValue === undefined && pathValue !== undefined) {
        return {
          kind: "readPath",
          path: asString(pathValue, `event ${eventId} path`),
        };
      }
      return {
        kind: "read",
        handle: asString(handleValue, `event ${eventId} handle`),
        path:
          pathValue === undefined
            ? undefined
            : asString(pathValue, `event ${eventId} path`),
      };
    case "readPath":
      return {
        kind,
        path: asString(pathValue, `event ${eventId} path`),
      };
    case "unlink":
      return {
        kind,
        path: asString(pathValue, `event ${eventId} path`),
      };
    case "rename":
    case "link":
      return {
        kind,
        from: asString(fromValue, `event ${eventId} from`),
        to: asString(toValue, `event ${eventId} to`),
      };
    case "setClass": {
      const targetClass = optionalClass(
        input.class ?? input.protectionClass,
        `event ${eventId} class`,
      );
      if (targetClass === undefined) {
        throw new GraphError(`event ${eventId} setClass requires a class`);
      }
      return {
        kind,
        class: targetClass,
        path:
          pathValue === undefined
            ? undefined
            : asString(pathValue, `event ${eventId} path`),
        handle:
          handleValue === undefined
            ? undefined
            : asString(handleValue, `event ${eventId} handle`),
      };
    }
    case "copy":
      return {
        kind,
        handle: asString(handleValue, `event ${eventId} handle`),
        to: asString(toValue ?? pathValue, `event ${eventId} to`),
        protectionClass: optionalClass(
          input.protectionClass ?? input.class,
          `event ${eventId} protectionClass`,
        ),
        contentLabel: optionalClass(
          input.contentLabel ?? input.label,
          `event ${eventId} contentLabel`,
        ),
      };
    case "lock":
      return { kind: "lock" };
    case "unlock":
      return { kind: "unlock" };
    default:
      throw new GraphError(`event ${eventId} has unknown operation ${kind}`);
  }
}

function normalizeEvent(raw: EventSpec, index: number): NormalizedEvent {
  if (raw === null || typeof raw !== "object") {
    throw new GraphError(`event at index ${index} is not an object`);
  }
  const id = asString(raw.id, `event at index ${index} id`);
  const before = new Set<EventId>();
  const loose = raw as EventSpec & {
    readonly requires?: readonly EventId[];
    readonly predecessors?: readonly EventId[];
    readonly operation?: unknown;
    readonly action?: unknown;
  };
  for (const list of [
    raw.before,
    raw.deps,
    loose.requires,
    loose.predecessors,
  ]) {
    if (list === undefined) {
      continue;
    }
    if (!Array.isArray(list)) {
      throw new GraphError(`event ${id} before/deps must be an array`);
    }
    for (const dependency of list) {
      before.add(asString(dependency, `event ${id} dependency`));
    }
  }
  if (raw.after !== undefined) {
    if (!Array.isArray(raw.after)) {
      throw new GraphError(`event ${id} after must be an array`);
    }
    // `after` is inverted into a predecessor relation by the caller after all
    // ids are known.  Keep it on the provisional object here.
  }
  return {
    id,
    op: normalizeOperation(
      raw.op ?? loose.operation ?? loose.action,
      id,
    ),
    before,
    index,
  };
}

function resolveConfig(
  graph: OperationGraph,
  options: AnalyzeOptions,
): ResolvedConfig {
  const volumeDefaults = new Map<VolumeId, ProtectionClass>();
  const graphVolumes = graph.volumes ?? {};
  const optionVolumes = options.volumes ?? {};
  for (const [volume, value] of Object.entries(graphVolumes)) {
    volumeDefaults.set(volume, normalizeVolumeConfig(value, volume));
  }
  for (const [volume, value] of Object.entries(optionVolumes)) {
    volumeDefaults.set(volume, normalizeVolumeConfig(value, volume));
  }

  const pathVolumes = new Map<PathId, VolumeId>();
  for (const path of graph.paths ?? []) {
    if (path.volume !== undefined) {
      pathVolumes.set(
        asString(path.id, "path spec id"),
        asString(path.volume, `path ${path.id} volume`),
      );
    }
  }
  for (const [path, volume] of Object.entries(graph.pathVolumes ?? {})) {
    pathVolumes.set(path, asString(volume, `path ${path} volume`));
  }
  for (const [path, volume] of Object.entries(options.pathVolumes ?? {})) {
    pathVolumes.set(path, asString(volume, `path ${path} volume`));
  }

  const defaultVolume =
    options.defaultVolume ?? graph.defaultVolume ?? "default";
  const defaultClass =
    options.defaultClass ?? graph.defaultClass ?? "public";
  if (!isProtectionClass(defaultClass)) {
    throw new GraphError("defaultClass is not a protection class");
  }
  const resolver = options.resolveVolume ?? graph.resolveVolume;
  return {
    volumeDefaults,
    pathVolumes,
    resolver,
    defaultVolume,
    defaultClass,
  };
}

function normalizeInitial(
  graph: OperationGraph,
  config: ResolvedConfig,
): InitialPathSpec[] {
  const result: InitialPathSpec[] = [];
  const paths = new Set<PathId>();
  const objectIds = new Set<ObjectId>();
  let index = 0;
  for (const entry of graph.initial ?? []) {
    const path = asString(entry.path, `initial path at index ${index}`);
    if (paths.has(path)) {
      throw new GraphError(`duplicate initial path ${path}`);
    }
    paths.add(path);
    const protectionClass =
      optionalClass(entry.protectionClass ?? entry.class, `initial ${path} class`) ??
      volumeDefaultClass(config, entry.volume ?? resolveVolumeId(path, config));
    const contentLabel =
      optionalClass(entry.contentLabel, `initial ${path} contentLabel`) ??
      protectionClass;
    const objectId = entry.objectId ?? `init:${index}`;
    if (objectIds.has(objectId)) {
      throw new GraphError(`duplicate initial object id ${objectId}`);
    }
    objectIds.add(objectId);
    result.push({
      path,
      protectionClass,
      contentLabel,
      volume: entry.volume ?? resolveVolumeId(path, config),
      objectId,
    });
    index += 1;
  }
  return result;
}

export function normalizeGraph(
  graph: OperationGraph,
  options: AnalyzeOptions = {},
): NormalizedGraph {
  if (graph === null || typeof graph !== "object") {
    throw new GraphError("graph must be an object");
  }
  if (!Array.isArray(graph.events)) {
    throw new GraphError("graph.events must be an array");
  }
  const maxEvents = Math.min(
    options.maxEvents ?? MAX_SPEC_EVENTS,
    MAX_SPEC_EVENTS,
  );
  if (graph.events.length > maxEvents) {
    throw new GraphError(
      `graph has ${graph.events.length} events; maximum is ${maxEvents}`,
    );
  }

  const provisional = graph.events.map((event, index) =>
    normalizeEvent(event, index),
  );
  const byId = new Map<EventId, NormalizedEvent>();
  for (const event of provisional) {
    if (byId.has(event.id)) {
      throw new GraphError(`duplicate event id ${event.id}`);
    }
    byId.set(event.id, event);
  }

  // Fold `after` edges into `before` edges now that all ids are known.
  const beforeById = new Map<EventId, Set<EventId>>();
  for (const event of provisional) {
    beforeById.set(event.id, new Set(event.before));
  }
  for (const raw of graph.events) {
    const id = asString(raw.id, "event id");
    for (const successor of raw.after ?? []) {
      const successorId = asString(successor, `event ${id} after`);
      if (!byId.has(successorId)) {
        throw new GraphError(
          `event ${id} references unknown successor ${successorId}`,
        );
      }
      beforeById.get(successorId)?.add(id);
    }
  }
  for (const [id, before] of beforeById) {
    for (const dependency of before) {
      if (!byId.has(dependency)) {
        throw new GraphError(
          `event ${id} references unknown predecessor ${dependency}`,
        );
      }
      if (dependency === id) {
        throw new GraphError(`event ${id} depends on itself`);
      }
    }
  }

  // A partial order must be acyclic.  Kahn's algorithm also catches cycles
  // hidden behind `after` edges.
  const indegree = new Map<EventId, number>();
  const successors = new Map<EventId, EventId[]>();
  for (const id of byId.keys()) {
    indegree.set(id, 0);
    successors.set(id, []);
  }
  for (const [id, before] of beforeById) {
    for (const dependency of before) {
      indegree.set(id, (indegree.get(id) ?? 0) + 1);
      const list = successors.get(dependency) ?? [];
      list.push(id);
      successors.set(dependency, list);
    }
  }
  const ready: EventId[] = [...indegree.entries()]
    .filter(([, degree]) => degree === 0)
    .map(([id]) => id);
  let visited = 0;
  while (ready.length > 0) {
    const id = ready.pop() as EventId;
    visited += 1;
    for (const successor of successors.get(id) ?? []) {
      const next = (indegree.get(successor) ?? 0) - 1;
      indegree.set(successor, next);
      if (next === 0) {
        ready.push(successor);
      }
    }
  }
  if (visited !== byId.size) {
    throw new GraphError("event graph contains a cycle");
  }

  const compareEventIds = options.compareEventIds ?? defaultCompare;
  const events: NormalizedEvent[] = provisional.map((event) => ({
    id: event.id,
    op: event.op,
    before: beforeById.get(event.id) ?? new Set<EventId>(),
    index: event.index,
  }));
  const order = events
    .map((event) => event.id)
    .sort((left, right) => compareEventIds(left, right));

  const config = resolveConfig(graph, options);
  const initial = normalizeInitial(graph, config);
  const generatedObjectIds = new Set(
    events.map((event) => `obj:${event.id}`),
  );
  for (const entry of initial) {
    if (entry.objectId !== undefined && generatedObjectIds.has(entry.objectId)) {
      throw new GraphError(
        `initial object id ${entry.objectId} collides with a generated object`,
      );
    }
  }
  const canonicalEvents = events.map((event) => ({
    id: event.id,
    op: canonicalOperation(event.op),
    before: [...event.before].sort(defaultCompare),
  }));
  const digest = digestValue({
    events: canonicalEvents,
    initial: initial.map((entry) => ({
      path: entry.path,
      protectionClass: entry.protectionClass,
      contentLabel: entry.contentLabel,
      volume: entry.volume,
      objectId: entry.objectId,
    })),
    volumes: [...config.volumeDefaults.entries()].sort((left, right) =>
      defaultCompare(left[0], right[0]),
    ),
    pathVolumes: [...config.pathVolumes.entries()].sort((left, right) =>
      defaultCompare(left[0], right[0]),
    ),
    resolvedPathVolumes: collectPaths(events, initial).map((path) => [
      path,
      resolveVolumeId(path, config),
    ]),
    defaultVolume: config.defaultVolume,
    defaultClass: config.defaultClass,
  });

  return {
    events,
    eventById: byId,
    order,
    initial,
    config,
    digest,
    compareEventIds,
  };
}

function canonicalOperation(op: Operation): unknown {
  switch (op.kind) {
    case "create":
    case "restore":
    case "replace":
      return {
        kind: op.kind,
        path: op.path,
        protectionClass: op.protectionClass,
        contentLabel: op.contentLabel,
      };
    case "open":
      return { kind: op.kind, path: op.path, handle: op.handle };
    case "close":
      return { kind: op.kind, handle: op.handle };
    case "read":
      return { kind: op.kind, handle: op.handle, path: op.path };
    case "readPath":
      return { kind: op.kind, path: op.path };
    case "unlink":
      return { kind: op.kind, path: op.path };
    case "rename":
    case "link":
      return { kind: op.kind, from: op.from, to: op.to };
    case "setClass":
      return {
        kind: op.kind,
        class: op.class,
        path: op.path,
        handle: op.handle,
      };
    case "copy":
      return {
        kind: op.kind,
        handle: op.handle,
        to: op.to,
        protectionClass: op.protectionClass,
        contentLabel: op.contentLabel,
      };
    case "lock":
      return { kind: "lock" };
    case "unlock":
      return { kind: "unlock" };
  }
}

function collectPaths(
  events: readonly NormalizedEvent[],
  initial: readonly InitialPathSpec[],
): string[] {
  const paths = new Set<string>();
  for (const entry of initial) {
    paths.add(entry.path);
  }
  for (const event of events) {
    const op = event.op;
    switch (op.kind) {
      case "create":
      case "restore":
      case "replace":
      case "open":
      case "readPath":
      case "unlink":
        paths.add(op.path);
        break;
      case "read":
        if (op.path !== undefined) {
          paths.add(op.path);
        }
        break;
      case "rename":
      case "link":
        paths.add(op.from);
        paths.add(op.to);
        break;
      case "copy":
        paths.add(op.to);
        break;
      case "setClass":
        if (op.path !== undefined) {
          paths.add(op.path);
        }
        break;
      default:
        break;
    }
  }
  return [...paths].sort(defaultCompare);
}

export function resolveVolumeId(
  path: PathId,
  config: ResolvedConfig,
): VolumeId {
  return (
    config.pathVolumes.get(path) ??
    config.resolver?.(path) ??
    config.defaultVolume
  );
}

export function volumeDefaultClass(
  config: ResolvedConfig,
  volume: VolumeId,
): ProtectionClass {
  return config.volumeDefaults.get(volume) ?? config.defaultClass;
}

export function resolveVolume(
  path: PathId,
  config: ResolvedConfig,
): ResolvedVolume {
  const id = resolveVolumeId(path, config);
  return { id, defaultClass: volumeDefaultClass(config, id) };
}

function isNormalizedGraph(value: unknown): value is NormalizedGraph {
  return (
    value !== null &&
    typeof value === "object" &&
    "eventById" in value &&
    "config" in value &&
    "digest" in value
  );
}

export function buildInitialState(
  graph: OperationGraph | NormalizedGraph,
  options: AnalyzeOptions = {},
): RuntimeState {
  const normalized = isNormalizedGraph(graph)
    ? graph
    : normalizeGraph(graph, options);
  const objects = new Map<ObjectId, ObjectRecord>();
  const paths = new Map<PathId, ObjectId>();
  for (const entry of normalized.initial) {
    const protectionClass = entry.protectionClass;
    const contentLabel = entry.contentLabel;
    if (protectionClass === undefined || contentLabel === undefined) {
      throw new GraphError(`initial path ${entry.path} is not fully normalized`);
    }
    const objectId = entry.objectId ?? `init:${paths.size}`;
    objects.set(objectId, {
      id: objectId,
      protectionClass,
      contentLabel,
      volume: entry.volume ?? resolveVolumeId(entry.path, normalized.config),
    });
    paths.set(entry.path, objectId);
  }
  return {
    objects,
    paths,
    handles: new Map(),
    locked: false,
    lockSerial: 0,
    executed: new Set(),
  };
}

export function stateKey(state: RuntimeState): string {
  const objects = [...state.objects.values()]
    .sort((left, right) => defaultCompare(left.id, right.id))
    .map((object) => [
      object.id,
      object.protectionClass,
      object.contentLabel,
      object.volume,
    ]);
  const paths = [...state.paths.entries()].sort((left, right) =>
    defaultCompare(left[0], right[0]),
  );
  const handles = [...state.handles.values()]
    .sort((left, right) => defaultCompare(left.id, right.id))
    .map((handle) => [
      handle.id,
      handle.objectId,
      handle.openedAtLockSerial,
      handle.valid,
    ]);
  const executed = [...state.executed].sort(defaultCompare);
  return JSON.stringify({
    objects,
    paths,
    handles,
    locked: state.locked,
    lockSerial: state.lockSerial,
    executed,
  });
}

function cloneState(state: RuntimeState): {
  objects: Map<ObjectId, ObjectRecord>;
  paths: Map<PathId, ObjectId>;
  handles: Map<HandleId, HandleRecord>;
  locked: boolean;
  lockSerial: number;
  executed: Set<EventId>;
} {
  return {
    objects: new Map(state.objects),
    paths: new Map(state.paths),
    handles: new Map(state.handles),
    locked: state.locked,
    lockSerial: state.lockSerial,
    executed: new Set(state.executed),
  };
}

function markExecuted(state: RuntimeState, event: EventId): RuntimeState {
  return {
    objects: state.objects,
    paths: state.paths,
    handles: state.handles,
    locked: state.locked,
    lockSerial: state.lockSerial,
    executed: new Set([...state.executed, event]),
  };
}

function rejection(event: EventId, code: string, message: string): Rejection {
  return { event, code, message };
}

function reject(
  state: RuntimeState,
  event: EventId,
  code: string,
  message: string,
): ApplyResult {
  return {
    state: markExecuted(state, event),
    outcome: "rejected",
    rejection: rejection(event, code, message),
  };
}

type ReadAuthorization =
  | { readonly ok: true; readonly object: ObjectRecord }
  | { readonly ok: false; readonly code: string; readonly message: string };

function authorizeHandleRead(
  state: RuntimeState,
  handleId: HandleId,
): ReadAuthorization {
  const handle = state.handles.get(handleId);
  if (handle === undefined) {
    return {
      ok: false,
      code: "handle-missing",
      message: `handle ${handleId} does not exist`,
    };
  }
  if (!handle.valid) {
    return {
      ok: false,
      code: "handle-closed",
      message: `handle ${handleId} is closed`,
    };
  }
  const object = state.objects.get(handle.objectId);
  if (object === undefined) {
    return {
      ok: false,
      code: "object-missing",
      message: `handle ${handleId} refers to a missing object`,
    };
  }
  switch (object.protectionClass) {
    case "public":
      return { ok: true, object };
    case "untilLock":
      if (!state.locked || handle.openedAtLockSerial < state.lockSerial) {
        return { ok: true, object };
      }
      return {
        ok: false,
        code: "locked-until-lock-handle",
        message: `handle ${handleId} was opened after the current lock`,
      };
    case "complete":
      if (state.locked) {
        return {
          ok: false,
          code: "locked-complete",
          message: "complete-class objects cannot be read while locked",
        };
      }
      return { ok: true, object };
  }
  throw new Error("unreachable protection class");
}

function authorizePathRead(
  state: RuntimeState,
  path: PathId,
): ReadAuthorization {
  const objectId = state.paths.get(path);
  if (objectId === undefined) {
    return {
      ok: false,
      code: "path-missing",
      message: `path ${path} does not exist`,
    };
  }
  const object = state.objects.get(objectId);
  if (object === undefined) {
    return {
      ok: false,
      code: "object-missing",
      message: `path ${path} refers to a missing object`,
    };
  }
  switch (object.protectionClass) {
    case "public":
      return { ok: true, object };
    case "untilLock":
      // A path read opens a fresh handle at the current lock serial, so it is
      // never one of "this lock's pre-lock handles".
      if (!state.locked) {
        return { ok: true, object };
      }
      return {
        ok: false,
        code: "locked-until-lock-handle",
        message: "path reads cannot use a pre-lock handle",
      };
    case "complete":
      if (state.locked) {
        return {
          ok: false,
          code: "locked-complete",
          message: "complete-class objects cannot be read while locked",
        };
      }
      return { ok: true, object };
  }
  throw new Error("unreachable protection class");
}

function violationFor(
  state: RuntimeState,
  event: EventId,
  kind: "read" | "readPath" | "copy",
  object: ObjectRecord,
  handle?: HandleId,
  path?: PathId,
): ViolationDetail | undefined {
  if (!state.locked || object.contentLabel !== "complete") {
    return undefined;
  }
  return {
    event,
    kind,
    objectId: object.id,
    handle,
    path,
    protectionClass: object.protectionClass,
    contentLabel: object.contentLabel,
    lockSerial: state.lockSerial,
  };
}

function objectPaths(state: RuntimeState, objectId: ObjectId): number {
  let count = 0;
  for (const mapped of state.paths.values()) {
    if (mapped === objectId) {
      count += 1;
    }
  }
  return count;
}

function validateClass(
  value: ProtectionClass,
  event: EventId,
): string | undefined {
  return isProtectionClass(value)
    ? undefined
    : `event ${event} has an invalid protection class`;
}

export function applyEvent(
  state: RuntimeState,
  event: NormalizedEvent,
  graph: NormalizedGraph,
): ApplyResult {
  const op = event.op;
  const id = event.id;

  switch (op.kind) {
    case "create": {
      if (state.paths.has(op.path)) {
        return reject(state, id, "path-exists", `path ${op.path} already exists`);
      }
      const volume = resolveVolume(op.path, graph.config);
      const protectionClass = op.protectionClass ?? volume.defaultClass;
      const contentLabel = op.contentLabel ?? protectionClass;
      const invalid = validateClass(protectionClass, id) ?? validateClass(contentLabel, id);
      if (invalid !== undefined) {
        return reject(state, id, "invalid-class", invalid);
      }
      const next = cloneState(state);
      const objectId = `obj:${id}`;
      next.objects.set(objectId, {
        id: objectId,
        protectionClass,
        contentLabel,
        volume: volume.id,
      });
      next.paths.set(op.path, objectId);
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
    case "restore": {
      const volume = resolveVolume(op.path, graph.config);
      const protectionClass = op.protectionClass ?? volume.defaultClass;
      const contentLabel = op.contentLabel ?? protectionClass;
      const invalid = validateClass(protectionClass, id) ?? validateClass(contentLabel, id);
      if (invalid !== undefined) {
        return reject(state, id, "invalid-class", invalid);
      }
      const next = cloneState(state);
      const objectId = `obj:${id}`;
      next.objects.set(objectId, {
        id: objectId,
        protectionClass,
        contentLabel,
        volume: volume.id,
      });
      next.paths.set(op.path, objectId);
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
    case "replace": {
      if (!state.paths.has(op.path)) {
        return reject(state, id, "path-missing", `path ${op.path} does not exist`);
      }
      const volume = resolveVolume(op.path, graph.config);
      const protectionClass = op.protectionClass ?? volume.defaultClass;
      const contentLabel = op.contentLabel ?? protectionClass;
      const invalid = validateClass(protectionClass, id) ?? validateClass(contentLabel, id);
      if (invalid !== undefined) {
        return reject(state, id, "invalid-class", invalid);
      }
      const next = cloneState(state);
      const objectId = `obj:${id}`;
      next.objects.set(objectId, {
        id: objectId,
        protectionClass,
        contentLabel,
        volume: volume.id,
      });
      next.paths.set(op.path, objectId);
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
    case "open": {
      if (!state.paths.has(op.path)) {
        return reject(state, id, "path-missing", `path ${op.path} does not exist`);
      }
      const current = state.handles.get(op.handle);
      if (current !== undefined && current.valid) {
        return reject(
          state,
          id,
          "handle-in-use",
          `handle ${op.handle} is already open`,
        );
      }
      const objectId = state.paths.get(op.path);
      if (objectId === undefined) {
        return reject(state, id, "path-missing", `path ${op.path} does not exist`);
      }
      const next = cloneState(state);
      next.handles.set(op.handle, {
        id: op.handle,
        objectId,
        openedAtLockSerial: state.lockSerial,
        valid: true,
      });
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
    case "close": {
      const handle = state.handles.get(op.handle);
      if (handle === undefined) {
        return reject(
          state,
          id,
          "handle-missing",
          `handle ${op.handle} does not exist`,
        );
      }
      if (!handle.valid) {
        return reject(
          state,
          id,
          "handle-closed",
          `handle ${op.handle} is already closed`,
        );
      }
      const next = cloneState(state);
      next.handles.set(op.handle, { ...handle, valid: false });
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
    case "read": {
      const authorization = authorizeHandleRead(state, op.handle);
      if (!authorization.ok) {
        return reject(state, id, authorization.code, authorization.message);
      }
      // `path` is diagnostic only for a handle read.  The handle remains the
      // authority, so unlinking or replacing the path does not break it.
      const path =
        op.path !== undefined &&
        state.paths.get(op.path) === authorization.object.id
          ? op.path
          : undefined;
      const violation = violationFor(
        state,
        id,
        "read",
        authorization.object,
        op.handle,
        path,
      );
      return {
        state: markExecuted(state, id),
        outcome: "applied",
        violation,
      };
    }
    case "readPath": {
      const authorization = authorizePathRead(state, op.path);
      if (!authorization.ok) {
        return reject(state, id, authorization.code, authorization.message);
      }
      const violation = violationFor(
        state,
        id,
        "readPath",
        authorization.object,
        undefined,
        op.path,
      );
      return {
        state: markExecuted(state, id),
        outcome: "applied",
        violation,
      };
    }
    case "unlink": {
      if (!state.paths.has(op.path)) {
        return reject(state, id, "path-missing", `path ${op.path} does not exist`);
      }
      const next = cloneState(state);
      next.paths.delete(op.path);
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
    case "rename": {
      if (!state.paths.has(op.from)) {
        return reject(
          state,
          id,
          "path-missing",
          `path ${op.from} does not exist`,
        );
      }
      if (op.from === op.to) {
        return { state: markExecuted(state, id), outcome: "applied" };
      }
      const next = cloneState(state);
      const objectId = next.paths.get(op.from);
      next.paths.delete(op.from);
      if (objectId !== undefined) {
        next.paths.set(op.to, objectId);
      }
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
    case "link": {
      if (!state.paths.has(op.from)) {
        return reject(
          state,
          id,
          "path-missing",
          `path ${op.from} does not exist`,
        );
      }
      if (state.paths.has(op.to)) {
        return reject(state, id, "path-exists", `path ${op.to} already exists`);
      }
      if (op.from === op.to) {
        return reject(state, id, "path-exists", `path ${op.to} already exists`);
      }
      const objectId = state.paths.get(op.from);
      if (objectId === undefined) {
        return reject(
          state,
          id,
          "path-missing",
          `path ${op.from} does not exist`,
        );
      }
      const next = cloneState(state);
      next.paths.set(op.to, objectId);
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
    case "setClass": {
      let object: ObjectRecord | undefined;
      if (op.path !== undefined) {
        const objectId = state.paths.get(op.path);
        if (objectId === undefined) {
          return reject(
            state,
            id,
            "path-missing",
            `path ${op.path} does not exist`,
          );
        }
        object = state.objects.get(objectId);
      }
      if (op.handle !== undefined) {
        const handle = state.handles.get(op.handle);
        if (handle === undefined) {
          return reject(
            state,
            id,
            "handle-missing",
            `handle ${op.handle} does not exist`,
          );
        }
        if (!handle.valid) {
          return reject(
            state,
            id,
            "handle-closed",
            `handle ${op.handle} is closed`,
          );
        }
        const byHandle = state.objects.get(handle.objectId);
        if (object !== undefined && object.id !== handle.objectId) {
          return reject(
            state,
            id,
            "target-mismatch",
            `path ${op.path} and handle ${op.handle} name different objects`,
          );
        }
        object = byHandle;
      }
      if (object === undefined) {
        return reject(
          state,
          id,
          "target-missing",
          "setClass needs a path or handle",
        );
      }
      const invalid = validateClass(op.class, id);
      if (invalid !== undefined) {
        return reject(state, id, "invalid-class", invalid);
      }
      if (
        classRank(op.class) < classRank(object.protectionClass) &&
        objectPaths(state, object.id) > 1
      ) {
        return reject(
          state,
          id,
          "alias-downgrade",
          "cannot lower a protection class while the object has path aliases",
        );
      }
      if (op.class === object.protectionClass) {
        return { state: markExecuted(state, id), outcome: "applied" };
      }
      const next = cloneState(state);
      next.objects.set(object.id, { ...object, protectionClass: op.class });
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
    case "copy": {
      const authorization = authorizeHandleRead(state, op.handle);
      if (!authorization.ok) {
        return reject(state, id, authorization.code, authorization.message);
      }
      const source = authorization.object;
      if (
        op.contentLabel !== undefined &&
        classRank(op.contentLabel) < classRank(source.contentLabel)
      ) {
        return reject(
          state,
          id,
          "content-label-downgrade",
          "a copy cannot lower the source content label",
        );
      }
      const volume = resolveVolume(op.to, graph.config);
      const protectionClass = op.protectionClass ?? volume.defaultClass;
      const contentLabel = op.contentLabel ?? source.contentLabel;
      const invalid = validateClass(protectionClass, id) ?? validateClass(contentLabel, id);
      if (invalid !== undefined) {
        return reject(state, id, "invalid-class", invalid);
      }
      const violation = violationFor(
        state,
        id,
        "copy",
        source,
        op.handle,
        op.to,
      );
      const next = cloneState(state);
      const objectId = `obj:${id}`;
      next.objects.set(objectId, {
        id: objectId,
        protectionClass,
        contentLabel,
        volume: volume.id,
      });
      next.paths.set(op.to, objectId);
      next.executed.add(id);
      return { state: next, outcome: "applied", violation };
    }
    case "lock": {
      if (state.locked) {
        return reject(state, id, "already-locked", "the volume is already locked");
      }
      const next = cloneState(state);
      next.locked = true;
      next.lockSerial += 1;
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
    case "unlock": {
      if (!state.locked) {
        return reject(state, id, "already-unlocked", "the volume is not locked");
      }
      const next = cloneState(state);
      next.locked = false;
      next.executed.add(id);
      return { state: next, outcome: "applied" };
    }
  }
  throw new Error(`unreachable operation ${String((op as { kind?: string }).kind)}`);
}

export function replayPrefix(
  graph: OperationGraph | NormalizedGraph,
  prefix: readonly EventId[],
  options: AnalyzeOptions = {},
): { state: RuntimeState; steps: StepRecord[]; violation?: ViolationDetail } {
  const normalized = isNormalizedGraph(graph)
    ? graph
    : normalizeGraph(graph, options);
  let state = buildInitialState(normalized);
  const steps: StepRecord[] = [];
  let violation: ViolationDetail | undefined;
  for (const eventId of prefix) {
    const event = normalized.eventById.get(eventId);
    if (event === undefined) {
      throw new GraphError(`witness references unknown event ${eventId}`);
    }
    for (const dependency of event.before) {
      if (!state.executed.has(dependency)) {
        throw new GraphError(
          `witness executes ${eventId} before its predecessor ${dependency}`,
        );
      }
    }
    if (state.executed.has(eventId)) {
      throw new GraphError(`witness executes ${eventId} twice`);
    }
    const result = applyEvent(state, event, normalized);
    state = result.state;
    const step: StepRecord = {
      event: eventId,
      outcome: result.outcome,
      rejection: result.rejection,
      violation: result.violation,
    };
    steps.push(step);
    if (result.violation !== undefined) {
      violation = result.violation;
    }
  }
  return { state, steps, violation };
}

export function invalidResult(error: unknown): AnalysisResult {
  if (error instanceof GraphError) {
    return { status: "invalid", reason: error.message };
  }
  if (error instanceof Error) {
    return { status: "invalid", reason: error.message };
  }
  return { status: "invalid", reason: String(error) };
}
