import {
  previewWith,
  type Contribution,
  type FieldState,
} from "./fields.ts";
import {
  Graph,
  GraphError,
  PARENT_FIELD,
  WEIGHT_FIELD,
  type EntityNode,
} from "./graph.ts";
import {
  isPlainObject,
  normalize,
  pointerJoin,
  type JsonValue,
} from "./json.ts";

export interface SourceDef {
  id: string;
  /** Higher priority wins field conflicts. */
  priority: number;
}

export interface UpsertEvent {
  kind: "upsert";
  eventId: string;
  source: string;
  version: number;
  entity: {
    id: string;
    parentId: string | null;
    fields: Record<string, JsonValue>;
  };
}

export interface DeleteEvent {
  kind: "delete";
  eventId: string;
  source: string;
  version: number;
  entityId: string;
}

export type Event = UpsertEvent | DeleteEvent;

export interface Batch {
  batch: string;
  events: Event[];
}

export interface ApplyResult {
  batch: string;
  applied: string[];
  /** Duplicate event ids, skipped idempotently (never double-counted). */
  skipped: string[];
  errors: GraphError[];
}

export interface StoreConfig {
  sources: SourceDef[];
  /** Explicit delete policy; only "orphan" exists — children are never cascade-deleted. */
  deletePolicy: "orphan";
}

const ROOT = "";

function fail(code: string, message: string, path: string): never {
  throw new GraphError(code, message, path);
}

function asString(v: unknown, path: string): string {
  if (typeof v !== "string" || v.length === 0) {
    fail("VALIDATION", `expected non-empty string at ${path}`, path);
  }
  return v as string;
}

function asVersion(v: unknown, path: string): number {
  if (typeof v !== "number" || !Number.isInteger(v) || v < 0) {
    fail("VALIDATION", `expected non-negative integer version at ${path}`, path);
  }
  return v as number;
}

function asJsonValue(v: unknown, path: string): JsonValue {
  if (
    v === null ||
    typeof v === "boolean" ||
    typeof v === "string" ||
    (typeof v === "number" && Number.isFinite(v))
  ) {
    return v as JsonValue;
  }
  if (Array.isArray(v)) {
    return v.map((item, i) => asJsonValue(item, pointerJoin(path, i)));
  }
  if (isPlainObject(v)) {
    const out: Record<string, JsonValue> = {};
    for (const k of Object.keys(v)) {
      out[k] = asJsonValue(v[k], pointerJoin(path, k));
    }
    return out;
  }
  fail("VALIDATION", `value at ${path} is not JSON-compatible`, path);
}

/**
 * Store: applies multi-source batches of events into the graph.
 * Every event is validated first and committed second, so rejections
 * (cycles, stale versions, validation) are atomic — stats never move.
 */
export class Store {
  readonly graph = new Graph();
  readonly config: StoreConfig;
  private priorities = new Map<string, number>();
  private processed = new Set<string>();
  private history: { batch: string; eventId: string }[] = [];
  private lastErrors: GraphError[] = [];
  /** Errors raised while reattaching orphans after a commit. */
  private sideErrors: GraphError[] = [];

  constructor(config: StoreConfig) {
    this.config = config;
    for (const s of config.sources) this.priorities.set(s.id, s.priority);
  }

  private priorityOf = (source: string): number => {
    const p = this.priorities.get(source);
    if (p === undefined) {
      throw new GraphError("UNKNOWN_SOURCE", `unknown source ${source}`, ROOT);
    }
    return p;
  };

  /** Validate an event structurally, returning a typed event. */
  private parseEvent(raw: unknown, path: string): Event {
    if (!isPlainObject(raw)) fail("VALIDATION", `event at ${path} must be an object`, path);
    const kind = raw.kind;
    if (kind !== "upsert" && kind !== "delete") {
      fail("VALIDATION", `unknown event kind at ${pointerJoin(path, "kind")}`, pointerJoin(path, "kind"));
    }
    const eventId = asString(raw.eventId, pointerJoin(path, "eventId"));
    const source = asString(raw.source, pointerJoin(path, "source"));
    const version = asVersion(raw.version, pointerJoin(path, "version"));
    if (kind === "delete") {
      const entityId = asString(raw.entityId, pointerJoin(path, "entityId"));
      return { kind, eventId, source, version, entityId };
    }
    const entityPath = pointerJoin(path, "entity");
    const entity = raw.entity;
    if (!isPlainObject(entity)) fail("VALIDATION", `missing entity at ${entityPath}`, entityPath);
    const id = asString(entity.id, pointerJoin(entityPath, "id"));
    const parentPath = pointerJoin(entityPath, "parentId");
    const parentId = entity.parentId;
    if (parentId !== null && typeof parentId !== "string") {
      fail("VALIDATION", `parentId at ${parentPath} must be string|null`, parentPath);
    }
    const fieldsPath = pointerJoin(entityPath, "fields");
    const rawFields = entity.fields ?? {};
    if (!isPlainObject(rawFields)) fail("VALIDATION", `fields at ${fieldsPath} must be an object`, fieldsPath);
    const fields: Record<string, JsonValue> = {};
    for (const name of Object.keys(rawFields)) {
      if (name === PARENT_FIELD) fail("VALIDATION", `reserved field name at ${pointerJoin(fieldsPath, name)}`, pointerJoin(fieldsPath, name));
      fields[name] = asJsonValue(rawFields[name], pointerJoin(fieldsPath, name));
    }
    return { kind, eventId, source, version, entity: { id, parentId, fields } };
  }

  /** Apply one batch; a rejected event does not abort the rest of the batch. */
  applyBatch(raw: unknown): ApplyResult {
    if (!isPlainObject(raw)) fail("VALIDATION", "batch must be an object", ROOT);
    const batch = asString(raw.batch, "/batch");
    const rawEvents = raw.events;
    if (!Array.isArray(rawEvents)) fail("VALIDATION", "events must be an array", "/events");
    const result: ApplyResult = { batch, applied: [], skipped: [], errors: [] };
    rawEvents.forEach((rawEvent, i) => {
      const path = pointerJoin("/events", i);
      try {
        const event = this.parseEvent(rawEvent, path);
        if (this.processed.has(event.eventId)) {
          result.skipped.push(event.eventId);
          return;
        }
        this.priorityOf(event.source);
        if (event.kind === "upsert") this.applyUpsert(event, path);
        else this.applyDelete(event, path);
        this.processed.add(event.eventId);
        this.history.push({ batch, eventId: event.eventId });
        result.applied.push(event.eventId);
        for (const se of this.sideErrors) {
          se.batch = batch;
          result.errors.push(se);
        }
        this.sideErrors = [];
      } catch (e) {
        if (e instanceof GraphError) {
          e.batch = batch;
          result.errors.push(e);
        } else {
          throw e;
        }
      }
    });
    this.lastErrors = result.errors;
    return result;
  }

  /** Validate + preview an upsert, then commit. Nothing mutates before commit. */
  private applyUpsert(ev: UpsertEvent, path: string): void {
    const { id, parentId, fields } = ev.entity;
    let node = this.graph.nodes.get(id);
    const isNew = node === undefined;
    if (node) {
      const prev = node.sources.get(ev.source) ?? -1;
      if (ev.version <= prev) {
        fail(
          "STALE_VERSION",
          `version ${ev.version} <= applied ${prev} for ${id} from ${ev.source}`,
          pointerJoin(path, "version"),
        );
      }
    } else {
      node = this.graph.addEntity(id);
    }

    // Preview every touched field, including the reserved parent link.
    const entityPath = pointerJoin(path, "entity");
    const previews = new Map<string, FieldState>();
    previews.set(
      PARENT_FIELD,
      previewWith(
        node.fields.get(PARENT_FIELD),
        ev.source,
        {
          value: parentId,
          source: ev.source,
          version: ev.version,
          path: pointerJoin(entityPath, "parentId"),
        },
        this.priorityOf,
      ),
    );
    const fieldsPath = pointerJoin(entityPath, "fields");
    for (const name of Object.keys(fields)) {
      previews.set(
        name,
        previewWith(
          node.fields.get(name),
          ev.source,
          {
            value: fields[name]!,
            source: ev.source,
            version: ev.version,
            path: pointerJoin(fieldsPath, name),
          },
          this.priorityOf,
        ),
      );
    }

    // Cycle pre-check against the would-be effective parent (atomic reject).
    const parentPreview = previews.get(PARENT_FIELD)!;
    if (!parentPreview.conflict) {
      const next = parentPreview.effective?.value ?? null;
      if (typeof next === "string" && next !== node.parentId && this.graph.has(next)) {
        if (this.graph.wouldCycle(id, next)) {
          if (isNew) this.graph.nodes.delete(id);
          fail(
            "CYCLE",
            `moving ${id} under its descendant ${next} rejected`,
            pointerJoin(entityPath, "parentId"),
          );
        }
      }
    }

    // Commit.
    node.sources.set(ev.source, ev.version);
    for (const [name, state] of previews) node.fields.set(name, state);
    this.commitParent(node, parentPreview);
    this.commitWeight(node);
    if (isNew) this.graph.reattachOrphans(id, (e) => this.sideErrors.push(e));
  }

  /** Validate + preview a delete, then commit (entity may survive via other sources). */
  private applyDelete(ev: DeleteEvent, path: string): void {
    const node = this.graph.nodes.get(ev.entityId);
    if (!node) {
      fail("NO_ENTITY", `cannot delete unknown entity ${ev.entityId}`, pointerJoin(path, "entityId"));
    }
    const prev = node!.sources.get(ev.source);
    if (prev === undefined) {
      fail(
        "NO_CONTRIBUTION",
        `source ${ev.source} has no record of ${ev.entityId}`,
        pointerJoin(path, "source"),
      );
    }
    if (ev.version <= prev) {
      fail(
        "STALE_VERSION",
        `version ${ev.version} <= applied ${prev} for ${ev.entityId} from ${ev.source}`,
        pointerJoin(path, "version"),
      );
    }

    // Preview: drop this source's contribution from every field.
    const previews = new Map<string, FieldState>();
    for (const [name, state] of node!.fields) {
      if (state.contributions.has(ev.source)) {
        previews.set(name, previewWith(state, ev.source, null, this.priorityOf));
      }
    }
    const parentPreview = previews.get(PARENT_FIELD);
    if (parentPreview && !parentPreview.conflict) {
      const next = parentPreview.effective?.value ?? null;
      if (typeof next === "string" && next !== node!.parentId && this.graph.has(next)) {
        if (this.graph.wouldCycle(ev.entityId, next)) {
          fail("CYCLE", `delete would move ${ev.entityId} under its descendant ${next}`, pointerJoin(path, "entityId"));
        }
      }
    }

    // Commit.
    node!.sources.delete(ev.source);
    for (const [name, state] of previews) node!.fields.set(name, state);
    if (node!.sources.size === 0) {
      // No source left: entity removed, children kept per "orphan" policy.
      this.graph.removeEntity(ev.entityId);
      return;
    }
    if (parentPreview) this.commitParent(node!, parentPreview);
    this.commitWeight(node!);
  }

  /** Apply a committed parent preview: move, orphan, or detach. */
  private commitParent(node: EntityNode, parentState: FieldState): void {
    if (parentState.conflict) return; // undecided: keep current link, never arbitrary
    const next = parentState.effective?.value ?? null;
    if (next === null) {
      this.graph.clearParent(node.id);
      return;
    }
    if (typeof next !== "string") {
      throw new GraphError("VALIDATION", `effective parent must be string|null`, "");
    }
    if (next === node.parentId && node.pendingParentId === null) return;
    if (this.graph.has(next)) this.graph.attach(node.id, next);
    else this.graph.markOrphan(node.id, next);
  }

  /** Push the effective weight into subtree stats. */
  private commitWeight(node: EntityNode): void {
    const w = node.fields.get(WEIGHT_FIELD)?.effective?.value;
    this.graph.setSelfWeight(node.id, typeof w === "number" ? w : 0);
  }

  /** Field-level lineage for one entity (effective value, version, sources, conflicts). */
  lineage(entityId: string): Record<string, unknown> {
    const node = this.graph.get(entityId);
    const out: Record<string, unknown> = {};
    for (const [name, state] of node.fields) {
      out[name] = {
        effective: state.effective
          ? { value: state.effective.value, source: state.effective.source, version: state.effective.version, path: state.effective.path }
          : null,
        conflict: state.conflict
          ? state.conflict.map((c: Contribution) => ({ value: c.value, source: c.source, version: c.version, path: c.path }))
          : null,
        contributions: [...state.contributions.values()].map((c) => ({
          value: c.value,
          source: c.source,
          version: c.version,
          path: c.path,
        })),
      };
    }
    return out;
  }

  /** Serializable, normalized snapshot (excludes transient touch tracking). */
  snapshot(): JsonValue {
    const entities = [...this.graph.nodes.values()]
      .map((n) => ({
        id: n.id,
        parentId: n.parentId,
        pendingParentId: n.pendingParentId,
        children: [...n.children].sort(),
        sources: Object.fromEntries([...n.sources.entries()].sort(([a], [b]) => (a < b ? -1 : 1))),
        selfWeight: n.selfWeight,
        stats: { ...n.stats },
        fields: Object.fromEntries(
          [...n.fields.entries()]
            .sort(([a], [b]) => (a < b ? -1 : 1))
            .map(([name, s]) => [
              name,
              {
                effective: s.effective ? { ...s.effective } : null,
                conflict: s.conflict ? s.conflict.map((c) => ({ ...c })) : null,
                contributions: [...s.contributions.values()].map((c) => ({ ...c })),
              },
            ]),
        ),
      }))
      .sort((a, b) => (a.id < b.id ? -1 : 1));
    return normalize({
      config: {
        deletePolicy: this.config.deletePolicy,
        sources: this.config.sources.map((s) => ({ ...s })),
      },
      entities: entities as unknown as JsonValue,
      orphans: [...this.graph.orphans.entries()].sort(([a], [b]) => (a < b ? -1 : 1)),
      processed: [...this.processed].sort(),
      history: this.history.map((h) => ({ ...h })),
    } as JsonValue);
  }

  get errors(): GraphError[] {
    return this.lastErrors;
  }
}

