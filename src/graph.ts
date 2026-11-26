import {
  ALL_CAPABILITY_MASK,
  toMask,
  type CapabilityInput,
} from "./capabilities.ts";
import {
  InvalidDescriptionError,
  UnsafeDescriptionError,
  UnknownNodeError,
} from "./errors.ts";
import { FrozenMap } from "./immutable.ts";

export type Primitive = string | number | boolean | bigint | null | undefined;

export interface RefValue {
  readonly $ref: string;
}

export interface RefAlias {
  readonly ref: string;
}

export type DataValue = Primitive | RefValue | RefAlias;

export interface PropertySpec {
  readonly value?: DataValue;
  readonly ref?: string;
  readonly writable?: boolean;
  readonly enumerable?: boolean;
  readonly configurable?: boolean;
  /** Capabilities that can be carried by the returned child view. */
  readonly capabilities?: CapabilityInput;
  readonly caps?: CapabilityInput;
  /** Capabilities needed before the property is observable at all. */
  readonly requires?: CapabilityInput;
  /** Authorization edges that must all remain active for this property. */
  readonly grants?: string | readonly string[];
}

export interface CallSpec {
  readonly result?: DataValue;
  readonly returns?: DataValue;
  readonly value?: DataValue;
  readonly ref?: string;
  readonly throws?: string;
  readonly error?: string;
  readonly capabilities?: CapabilityInput;
  readonly caps?: CapabilityInput;
}

export interface PrototypeSpec {
  readonly ref?: string;
  readonly $ref?: string;
  readonly proto?: string;
  readonly prototype?: string;
  readonly capabilities?: CapabilityInput;
  readonly caps?: CapabilityInput;
  readonly requires?: CapabilityInput;
  readonly grants?: string | readonly string[];
}

export interface NodeSpec {
  readonly id?: string;
  readonly kind?: "object" | "array" | "function";
  readonly prototype?: string | PrototypeSpec | null;
  readonly proto?: string | PrototypeSpec | null;
  readonly properties?: Readonly<Record<string, PropertySpec | DataValue>>;
  readonly props?: Readonly<Record<string, PropertySpec | DataValue>>;
  readonly invoke?: CallSpec | DataValue;
  readonly call?: CallSpec | DataValue;
  readonly construct?: CallSpec | DataValue;
  readonly new?: CallSpec | DataValue;
}

export interface GraphSpec {
  readonly root?: string;
  readonly rootId?: string;
  readonly nodes?: readonly NodeSpec[] | Readonly<Record<string, NodeSpec>>;
  readonly objects?: readonly NodeSpec[] | Readonly<Record<string, NodeSpec>>;
  readonly definitions?:
    | readonly NodeSpec[]
    | Readonly<Record<string, NodeSpec>>;
}

export type NodeKind = "object" | "array" | "function";

export interface StoredPrimitive {
  readonly kind: "primitive";
  readonly value: Primitive;
}

export interface StoredRef {
  readonly kind: "ref";
  readonly ref: string;
}

export type StoredValue = StoredPrimitive | StoredRef;

export interface PrototypeLink {
  readonly ref: string;
  readonly capabilities: number;
  readonly requires: number;
  readonly grants: readonly string[];
}

export interface PropertyDef {
  readonly key: string;
  readonly value: StoredValue;
  readonly writable: boolean;
  readonly enumerable: boolean;
  readonly configurable: boolean;
  readonly capabilities: number;
  readonly requires: number;
  readonly grants: readonly string[];
  readonly order: number;
}

export interface CallDef {
  readonly result: StoredValue;
  readonly throws: string | null;
  readonly capabilities: number;
}

export interface NodeDef {
  readonly id: string;
  readonly kind: NodeKind;
  readonly prototype: PrototypeLink | null;
  readonly properties: ReadonlyMap<string, PropertyDef>;
  readonly invoke: CallDef | null;
  readonly construct: CallDef | null;
}

function isReference(value: unknown): value is RefValue | RefAlias {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const own = ownKeysOf(value as object);
  if (own.length === 0) return false;
  const hasDollarRef = own.includes("$ref");
  const hasRef = own.includes("ref");
  if (!hasDollarRef && !hasRef) return false;
  return own.every((key) => key === "$ref" || key === "ref");
}

function ownKeysOf(value: object): string[] {
  const keys: string[] = [];
  for (const key of Reflect.ownKeys(value)) {
    if (typeof key === "symbol") {
      throw new InvalidDescriptionError("Symbol-keyed description data is not supported");
    }
    keys.push(key);
  }
  return keys;
}

function readOwn(value: object, key: string): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined) return undefined;
  if ("get" in descriptor || "set" in descriptor) {
    throw new UnsafeDescriptionError(
      `Accessor "${key}" is executable and is not allowed in a description graph`,
    );
  }
  return descriptor.value;
}

function assertNoFunction(value: unknown, path: string, seen = new Set<object>()): void {
  if (typeof value === "function") {
    throw new UnsafeDescriptionError(
      `Function value found at ${path}; description graphs may only carry symbolic data`,
    );
  }
  if (value === null || typeof value !== "object") return;
  if (seen.has(value)) return;
  seen.add(value);
  for (const key of Reflect.ownKeys(value)) {
    const label = typeof key === "symbol" ? key.description ?? key.toString() : key;
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined) continue;
    if ("get" in descriptor || "set" in descriptor) {
      throw new UnsafeDescriptionError(
        `Accessor "${path}.${label}" is executable and is not allowed in a description graph`,
      );
    }
    assertNoFunction(descriptor.value, `${path}.${String(label)}`, seen);
  }
}

function yes(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback;
}

function normalizeStoredValue(value: unknown, path: string): StoredValue {
  if (typeof value === "function") {
    throw new UnsafeDescriptionError(
      `Function value found at ${path}; description graphs may only carry symbolic data`,
    );
  }
  if (value === null || value === undefined) {
    return Object.freeze({
      kind: "primitive" as const,
      value: value as null | undefined,
    });
  }
  switch (typeof value) {
    case "string":
    case "number":
    case "boolean":
    case "bigint":
      return Object.freeze({ kind: "primitive" as const, value });
    case "object": {
      if (isReference(value)) {
        const raw = readOwn(value, "$ref") ?? readOwn(value, "ref");
        if (typeof raw !== "string") {
          throw new InvalidDescriptionError(`Reference at ${path} must have a string id`);
        }
        return Object.freeze({ kind: "ref" as const, ref: raw });
      }
      throw new InvalidDescriptionError(
        `Value at ${path} must be a primitive or an explicit { $ref: "node" } reference`,
      );
    }
    default:
      throw new InvalidDescriptionError(`Unsupported value kind at ${path}`);
  }
}

function normalizeGrants(value: unknown, path: string): readonly string[] {
  if (value === undefined || value === null) return Object.freeze([]);
  const list = typeof value === "string" ? [value] : value;
  if (!Array.isArray(list)) {
    throw new InvalidDescriptionError(`"grants" at ${path} must be a string or string array`);
  }
  const result: string[] = [];
  for (const grant of list) {
    if (typeof grant !== "string") {
      throw new InvalidDescriptionError(`"grants" at ${path} must contain only strings`);
    }
    result.push(grant);
  }
  return Object.freeze(result);
}

function normalizeRequires(value: unknown, path: string): number {
  if (value === undefined || value === null) return 0;
  try {
    return toMask(value as CapabilityInput);
  } catch (error) {
    throw new InvalidDescriptionError(`Invalid "requires" at ${path}: ${String(error)}`);
  }
}

function normalizeCaps(value: unknown, path: string): number {
  try {
    return toMask(value as CapabilityInput);
  } catch (error) {
    throw new InvalidDescriptionError(`Invalid "capabilities" at ${path}: ${String(error)}`);
  }
}

function normalizePrototype(value: unknown, path: string): PrototypeLink | null {
  if (value === undefined || value === null) return null;
  if (typeof value === "string") {
    return Object.freeze({
      ref: value,
      capabilities: ALL_CAPABILITY_MASK,
      requires: 0,
      grants: Object.freeze([] as string[]),
    });
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new InvalidDescriptionError(`Invalid prototype at ${path}`);
  }
  const object = value as PrototypeSpec;
  const ref =
    readOwn(object, "$ref") ??
    readOwn(object, "ref") ??
    readOwn(object, "proto") ??
    readOwn(object, "prototype");
  if (typeof ref !== "string") {
    throw new InvalidDescriptionError(`Prototype at ${path} must name a node`);
  }
  return Object.freeze({
    ref,
    capabilities: normalizeCaps(
      readOwn(object, "capabilities") ?? readOwn(object, "caps"),
      `${path}.capabilities`,
    ),
    requires: normalizeRequires(readOwn(object, "requires"), `${path}.requires`),
    grants: normalizeGrants(readOwn(object, "grants"), path),
  });
}

function normalizeProperty(value: unknown, key: string, nodeId: string, order: number): PropertyDef {
  const path = `nodes.${nodeId}.properties.${key}`;
  if (typeof value === "function") {
    throw new UnsafeDescriptionError(`Function value found at ${path}`);
  }
  if (value === null || typeof value !== "object" || isReference(value) || Array.isArray(value)) {
    return Object.freeze({
      key,
      value: normalizeStoredValue(value, path),
      writable: true,
      enumerable: true,
      configurable: true,
      capabilities: ALL_CAPABILITY_MASK,
      requires: 0,
      grants: Object.freeze([] as string[]),
      order,
    });
  }

  const object = value as PropertySpec;
  if (readOwn(object, "get") !== undefined || readOwn(object, "set") !== undefined) {
    throw new UnsafeDescriptionError(
      `Accessor property at ${path} would require executing a carried function`,
    );
  }
  const rawValue = readOwn(object, "value");
  const rawRef = readOwn(object, "ref");
  const stored =
    rawValue !== undefined
      ? normalizeStoredValue(rawValue, `${path}.value`)
      : rawRef !== undefined
        ? normalizeStoredValue({ $ref: rawRef }, `${path}.ref`)
        : normalizeStoredValue(undefined, path);
  return Object.freeze({
    key,
    value: stored,
    writable: yes(readOwn(object, "writable"), true),
    enumerable: yes(readOwn(object, "enumerable"), true),
    configurable: yes(readOwn(object, "configurable"), true),
    capabilities: normalizeCaps(
      readOwn(object, "capabilities") ?? readOwn(object, "caps"),
      `${path}.capabilities`,
    ),
    requires: normalizeRequires(readOwn(object, "requires"), `${path}.requires`),
    grants: normalizeGrants(readOwn(object, "grants"), path),
    order,
  });
}

function normalizeCall(value: unknown, path: string): CallDef | null {
  if (value === undefined) return null;
  if (typeof value === "function") {
    throw new UnsafeDescriptionError(`Function value found at ${path}`);
  }
  if (value === null || typeof value !== "object" || isReference(value) || Array.isArray(value)) {
    return Object.freeze({
      result: normalizeStoredValue(value, path),
      throws: null,
      capabilities: ALL_CAPABILITY_MASK,
    });
  }
  const object = value as CallSpec;
  const rawResult =
    readOwn(object, "result") ??
    readOwn(object, "returns") ??
    readOwn(object, "value");
  const rawRef = readOwn(object, "ref");
  const throwsValue = readOwn(object, "throws") ?? readOwn(object, "error");
  if (
    throwsValue !== undefined &&
    throwsValue !== null &&
    typeof throwsValue !== "string"
  ) {
    throw new InvalidDescriptionError(`"throws" at ${path} must be a string`);
  }
  return Object.freeze({
    result:
      rawResult !== undefined
        ? normalizeStoredValue(rawResult, `${path}.result`)
        : rawRef !== undefined
          ? normalizeStoredValue({ $ref: rawRef }, `${path}.ref`)
          : normalizeStoredValue(undefined, path),
    throws: (throwsValue as string | undefined) ?? null,
    capabilities: normalizeCaps(
      readOwn(object, "capabilities") ?? readOwn(object, "caps"),
      `${path}.capabilities`,
    ),
  });
}

function normalizeNodes(
  specs: readonly NodeSpec[] | Readonly<Record<string, NodeSpec>>,
): NodeDef[] {
  const entries: Array<{ id: string; spec: NodeSpec }> = [];
  if (Array.isArray(specs)) {
    specs.forEach((spec, index) => {
      if (spec === null || typeof spec !== "object") {
        throw new InvalidDescriptionError(`Node at index ${index} must be an object`);
      }
      assertNoFunction(spec, `nodes[${index}]`);
      const id = readOwn(spec, "id");
      if (typeof id !== "string" || id.length === 0) {
        throw new InvalidDescriptionError(`Node at index ${index} needs a non-empty string id`);
      }
      entries.push({ id, spec });
    });
  } else if (specs !== null && typeof specs === "object") {
    for (const key of Reflect.ownKeys(specs)) {
      if (typeof key === "symbol") {
        throw new InvalidDescriptionError("Symbol-keyed node ids are not supported");
      }
    }
    for (const id of Object.keys(specs)) {
      if (id.length === 0) {
        throw new InvalidDescriptionError("Node ids must be non-empty strings");
      }
      const spec = readOwn(specs, id);
      if (spec === null || typeof spec !== "object" || Array.isArray(spec)) {
        throw new InvalidDescriptionError(`Node "${id}" must be an object`);
      }
      assertNoFunction(spec, `nodes.${id}`);
      const declaredId = readOwn(spec, "id");
      if (declaredId !== undefined && declaredId !== id) {
        throw new InvalidDescriptionError(
          `Node "${id}" declares a conflicting id "${String(declaredId)}"`,
        );
      }
      entries.push({ id, spec: spec as NodeSpec });
    }
  } else {
    throw new InvalidDescriptionError("Graph must provide a nodes array or record");
  }

  const seen = new Set<string>();
  const result: NodeDef[] = [];
  for (const { id, spec } of entries) {
    if (seen.has(id)) throw new InvalidDescriptionError(`Duplicate node id: ${id}`);
    seen.add(id);
    const rawKind = readOwn(spec, "kind");
    const rawInvoke = readOwn(spec, "invoke") ?? readOwn(spec, "call");
    const rawConstruct = readOwn(spec, "construct") ?? readOwn(spec, "new");
    const kind: NodeKind =
      rawKind === undefined
        ? rawInvoke !== undefined || rawConstruct !== undefined
          ? "function"
          : "object"
        : rawKind === "object" || rawKind === "array" || rawKind === "function"
          ? rawKind
          : (() => {
              throw new InvalidDescriptionError(`Invalid kind on node "${id}"`);
            })();

    const prototype = normalizePrototype(
      readOwn(spec, "prototype") ?? readOwn(spec, "proto"),
      `nodes.${id}.prototype`,
    );
    const rawProps = readOwn(spec, "properties") ?? readOwn(spec, "props");
    const properties = new Map<string, PropertyDef>();
    if (rawProps !== undefined) {
      if (rawProps === null || typeof rawProps !== "object" || Array.isArray(rawProps)) {
        throw new InvalidDescriptionError(`"properties" on node "${id}" must be a record`);
      }
      for (const key of Reflect.ownKeys(rawProps)) {
        if (typeof key === "symbol") {
          throw new InvalidDescriptionError(
            `Symbol-keyed property on node "${id}" is not supported`,
          );
        }
      }
      let order = 0;
      for (const key of Object.keys(rawProps)) {
        const propValue = readOwn(rawProps, key);
        if (properties.has(key)) {
          throw new InvalidDescriptionError(`Duplicate property "${key}" on node "${id}"`);
        }
        properties.set(key, normalizeProperty(propValue, key, id, order++));
      }
    }

        result.push(
          Object.freeze({
            id,
            kind,
            prototype,
            properties: new FrozenMap(properties),
            invoke: normalizeCall(rawInvoke, `nodes.${id}.invoke`),
            construct: normalizeCall(rawConstruct, `nodes.${id}.construct`),
      }),
    );
  }
  return result;
}

export class DescriptionGraph {
  readonly rootId: string;
  readonly nodes: ReadonlyMap<string, NodeDef>;

  constructor(
    spec: GraphSpec | readonly NodeSpec[] | Readonly<Record<string, NodeSpec>>,
    rootOverride?: string,
  ) {
    assertNoFunction(spec, "graph");
    let rawNodes: readonly NodeSpec[] | Readonly<Record<string, NodeSpec>> | undefined;
    let rawRoot: unknown;
    if (Array.isArray(spec)) {
      rawNodes = spec;
    } else if (spec !== null && typeof spec === "object") {
      const containerKeys = Object.keys(spec);
      const isWrapped =
        containerKeys.includes("nodes") ||
        containerKeys.includes("objects") ||
        containerKeys.includes("definitions") ||
        typeof readOwn(spec, "root") === "string" ||
        typeof readOwn(spec, "rootId") === "string";
      if (isWrapped) {
        rawRoot = readOwn(spec, "root") ?? readOwn(spec, "rootId");
        rawNodes =
          (readOwn(spec, "nodes") as
            | readonly NodeSpec[]
            | Readonly<Record<string, NodeSpec>>
            | undefined) ??
          (readOwn(spec, "objects") as
            | readonly NodeSpec[]
            | Readonly<Record<string, NodeSpec>>
            | undefined) ??
          (readOwn(spec, "definitions") as
            | readonly NodeSpec[]
            | Readonly<Record<string, NodeSpec>>
            | undefined);
      } else {
        rawNodes = spec as Readonly<Record<string, NodeSpec>>;
      }
    }
    if (rawNodes === undefined) {
      throw new InvalidDescriptionError("Description graph has no nodes");
    }

    const normalized = normalizeNodes(rawNodes);
    if (normalized.length === 0) {
      throw new InvalidDescriptionError("Description graph has no nodes");
    }
    const byId = new Map<string, NodeDef>(normalized.map((node) => [node.id, node]));

    for (const node of byId.values()) {
      if (node.prototype !== null && !byId.has(node.prototype.ref)) {
        throw new UnknownNodeError(node.prototype.ref);
      }
      for (const key of node.prototype?.grants ?? []) {
        if (typeof key !== "string") {
          throw new InvalidDescriptionError("Prototype grants must be strings");
        }
      }
      for (const property of node.properties.values()) {
        if (property.value.kind === "ref" && !byId.has(property.value.ref)) {
          throw new UnknownNodeError(property.value.ref);
        }
        if (property.value.kind === "primitive" && typeof property.value.value === "symbol") {
          throw new InvalidDescriptionError("Symbol primitive values are not supported");
        }
      }
      for (const call of [node.invoke, node.construct]) {
        if (call?.result.kind === "ref" && !byId.has(call.result.ref)) {
          throw new UnknownNodeError(call.result.ref);
        }
      }
    }

    for (const node of byId.values()) {
      const seen = new Set<string>([node.id]);
      let prototype = node.prototype;
      while (prototype !== null) {
        if (seen.has(prototype.ref)) {
          throw new InvalidDescriptionError(
            `Prototype cycle detected at node "${prototype.ref}"`,
          );
        }
        seen.add(prototype.ref);
        prototype = byId.get(prototype.ref)?.prototype ?? null;
      }
    }

    const selectedRoot = rootOverride ?? rawRoot;
    let rootId: string;
    if (selectedRoot === undefined) {
      rootId = normalized[0].id;
    } else {
      if (typeof selectedRoot !== "string" || !byId.has(selectedRoot)) {
        throw new InvalidDescriptionError(`Invalid graph root: ${String(selectedRoot)}`);
      }
      rootId = selectedRoot;
    }

    this.rootId = rootId;
    this.nodes = new FrozenMap(byId);
    Object.freeze(this);
  }

  get(id: string): NodeDef {
    const node = this.nodes.get(id);
    if (node === undefined) throw new UnknownNodeError(id);
    return node;
  }

  has(id: string): boolean {
    return this.nodes.has(id);
  }

  prototypeOf(id: string): PrototypeLink | null {
    return this.get(id).prototype;
  }
}
