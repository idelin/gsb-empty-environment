import {
  ALL_CAPABILITY_MASK,
  CapabilitySet,
  hasCapability,
  requireCapability,
  toMask,
  type Capability,
  type CapabilityInput,
} from "./capabilities.ts";
import {
  DescriptionGraph,
  type CallDef,
  type GraphSpec,
  type NodeDef,
  type NodeSpec,
  type Primitive,
  type PropertyDef,
  type StoredValue,
} from "./graph.ts";
import {
  PolicyEngine,
  type PolicyConfig,
  type PolicyEdgeDef,
  type PolicySpec,
  type RevocationAction,
} from "./policy.ts";
import type {
  BisimulationClass,
  FiniteStateModel,
} from "./bisim.ts";
import {
  CapabilityError,
  InvalidWriteError,
  PolicyError,
  RevokedViewError,
  SymbolicOperationError,
} from "./errors.ts";

export interface ViewOptions {
  readonly domain?: string;
  readonly policyNode?: string;
  readonly capabilities?: CapabilityInput;
}

export interface RewrapOptions extends ViewOptions {
  readonly edgeId?: string;
}

export interface RequestOptions extends ViewOptions {
  readonly delayMs?: number;
  readonly microtask?: boolean;
  /** Extra authorization edges the result must depend on. */
  readonly dependencies?: string | Iterable<string>;
}

export interface HostOptions {
  readonly graph:
    | GraphSpec
    | readonly NodeSpec[]
    | Readonly<Record<string, NodeSpec>>;
  readonly policy?: PolicySpec;
  readonly root?: string;
  readonly domain?: string;
  readonly policyNode?: string;
  readonly capabilities?: CapabilityInput;
}

export interface RevokeOptions {
  readonly invalidate?: boolean;
  readonly attenuate?: CapabilityInput;
  readonly deny?: CapabilityInput;
  readonly retain?: CapabilityInput;
}

export interface ViewSnapshot {
  readonly id: number;
  readonly nodeId: string;
  readonly domain: string;
  readonly policyNode: string;
  readonly capabilities: readonly Capability[];
  readonly support: readonly string[];
  readonly trace: readonly string[];
  readonly identity: number;
  readonly revoked: boolean;
  readonly revokedBy: string | null;
}

export interface RevocationEvent {
  readonly edgeId: string;
  readonly kind: "transfer" | "grant";
  readonly invalidated: readonly ViewSnapshot[];
  readonly downgraded: readonly ViewSnapshot[];
  readonly hiddenProperties: readonly string[];
}

export interface HostSnapshot {
  readonly rootId: string;
  readonly defaultDomain: string;
  readonly defaultPolicyNode: string;
  readonly policy: {
    readonly entry: string;
    readonly classCount: number;
    readonly configurationCount: number;
    readonly iterationCount: number;
    readonly nodes: readonly {
      readonly id: string;
      readonly domain: string;
      readonly capabilities: readonly Capability[];
    }[];
    readonly edges: readonly {
      readonly id: string;
      readonly from: string;
      readonly to: string;
      readonly kind: "transfer" | "grant";
      readonly revoked: boolean;
    }[];
  };
  readonly views: readonly ViewSnapshot[];
  readonly cache: {
    readonly identities: number;
    readonly leases: number;
  };
  readonly pendingRequests: number;
}

interface OverlayProperty {
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

interface ResolvedProperty {
  readonly prop: PropertyDef | OverlayProperty;
  readonly owner: string;
  readonly chainCaps: number;
  readonly chainRequires: number;
  readonly chainGrants: readonly string[];
}

interface IdentityRecord {
  readonly nodeId: string;
  readonly capsMask: number;
  readonly policyClass: BisimulationClass;
  readonly leases: Map<string, ViewRecord>;
  liveCount: number;
}

interface ViewRecord {
  readonly brand: true;
  readonly brandToken: object;
  readonly serial: number;
  readonly proxy: object;
  readonly target: object;
  readonly nodeId: string;
  config: PolicyConfig;
  capsMask: number;
  readonly support: Set<string>;
  readonly trace: Set<string>;
  anchors: readonly ViewRecord[];
  identity: IdentityRecord;
  leaseKey: string;
  revoked: boolean;
  revokedBy: string | null;
  epoch: number;
}

interface PendingRequest {
  readonly id: number;
  readonly deps: ReadonlySet<string>;
  readonly settle: () => unknown;
  readonly resolve: (value: unknown) => void;
  readonly reject: (error: unknown) => void;
  timer: ReturnType<typeof setTimeout> | null;
  settled: boolean;
}

interface RevocationListener {
  readonly deps: ReadonlySet<string> | null;
  readonly listener: (event: RevocationEvent) => void;
}

const VIEW_TAG = "BisimulationProxyView";
const INSPECT_SYMBOL = Symbol.for("nodejs.util.inspect.custom");
const VIEW_RECORD_BRAND = Symbol("bisimulation.proxy.record");

function supportKey(support: ReadonlySet<string>): string {
  return JSON.stringify([...support].sort());
}

function cloneSet(source: ReadonlySet<string>): Set<string> {
  return new Set(source);
}

function dependencyList(
  input: string | Iterable<string> | undefined,
): string[] {
  if (input === undefined) return [];
  return typeof input === "string" ? [input] : [...input];
}

function arrayIndex(key: string): number | null {
  if (key.length === 0) return null;
  if (key.length > 1 && key.charCodeAt(0) === 48) return null;
  const value = Number(key);
  if (!Number.isInteger(value) || value < 0 || value >= 0xffffffff) return null;
  return key === String(value) ? value : null;
}

function canonicalKeyCompare(left: string, right: string): number {
  const leftIndex = arrayIndex(left);
  const rightIndex = arrayIndex(right);
  if (leftIndex !== null && rightIndex !== null) return leftIndex - rightIndex;
  if (leftIndex !== null) return -1;
  if (rightIndex !== null) return 1;
  return left < right ? -1 : left > right ? 1 : 0;
}

function fromGraphProperty(prop: PropertyDef): OverlayProperty {
  return {
    key: prop.key,
    value: prop.value,
    writable: prop.writable,
    enumerable: prop.enumerable,
    configurable: prop.configurable,
    capabilities: prop.capabilities,
    requires: prop.requires,
    grants: prop.grants,
    order: prop.order,
  };
}

function isPrimitive(value: unknown): value is Primitive {
  return (
    value === null ||
    value === undefined ||
    typeof value === "string" ||
    typeof value === "number" ||
    typeof value === "boolean" ||
    typeof value === "bigint"
  );
}

function createProxyTarget(kind: "object" | "array" | "function"): object {
  if (kind === "function") {
    const dummy = function symbolicFunctionTarget(): void {
      throw new SymbolicOperationError(
        "The symbolic function target must never be executed",
      );
    };
    return dummy.bind(null);
  }
  return Object.create(null);
}

function graphGrants(graph: DescriptionGraph): string[] {
  const grants = new Set<string>();
  for (const node of graph.nodes.values()) {
    for (const grant of node.prototype?.grants ?? []) grants.add(grant);
    for (const property of node.properties.values()) {
      for (const grant of property.grants) grants.add(grant);
    }
  }
  return [...grants];
}

export class MultiDomainViewHost {
  readonly graph: DescriptionGraph;
  readonly policy: PolicyEngine;
  readonly defaultDomain: string;
  readonly defaultPolicyNode: string;
  readonly defaultCapabilities: number;

  readonly #records = new WeakMap<object, ViewRecord>();
  readonly #brandTokens = new WeakMap<object, ViewRecord>();
  readonly #live = new Set<ViewRecord>();
  readonly #identities = new Map<
    BisimulationClass,
    Map<string, Map<number, IdentityRecord>>
  >();
  readonly #semanticIdentityTokens = new Map<
    BisimulationClass,
    Map<string, Map<number, number>>
  >();
  readonly #overlay = new Map<string, Map<string, OverlayProperty>>();
  readonly #deleted = new Map<string, Set<string>>();
  readonly #pending = new Set<PendingRequest>();
  readonly #listeners = new Set<RevocationListener>();
  #nextViewSerial = 1;
  #nextSemanticIdentityToken = 1;
  #nextRequestId = 1;
  #nextOrder = 1_000_000;

  constructor(options: HostOptions) {
    this.graph = new DescriptionGraph(options.graph, options.root);
    const policySpec: PolicySpec =
      options.policy ??
      {
        entry: options.policyNode ?? "root",
        nodes: [
          {
            id: options.policyNode ?? "root",
            domain: options.domain ?? "A",
            capabilities: ALL_CAPABILITY_MASK,
          },
        ],
        edges: [],
      };
    this.policy = new PolicyEngine(policySpec, options.policyNode);

    if (options.policyNode !== undefined) {
      this.policy.node(options.policyNode);
    }
    this.defaultCapabilities = toMask(options.capabilities);
    const requestedDomain = options.domain;
    if (requestedDomain !== undefined) {
      const candidates = this.policy.nodesInDomain(requestedDomain);
      if (candidates.length === 0) {
        throw new PolicyError(`No policy node belongs to domain "${requestedDomain}"`);
      }
      if (options.policyNode !== undefined) {
        const selected = this.policy.node(options.policyNode);
        if (selected.domain !== requestedDomain) {
          throw new PolicyError(
            `Policy node "${selected.id}" belongs to "${selected.domain}", not "${requestedDomain}"`,
          );
        }
        this.defaultPolicyNode = selected.id;
      } else {
        this.defaultPolicyNode = this.#uniqueNodeInDomain(
          requestedDomain,
          this.defaultCapabilities,
        );
      }
    } else {
      this.defaultPolicyNode = options.policyNode ?? this.policy.entry;
    }
    this.defaultDomain = this.policy.node(this.defaultPolicyNode).domain;

    for (const grant of graphGrants(this.graph)) {
      if (!this.policy.edges.has(grant)) {
        throw new PolicyError(
          `Description graph refers to unknown authorization edge "${grant}"`,
        );
      }
    }
    Object.freeze(this);
  }

  get rootId(): string {
    return this.graph.rootId;
  }

  get rootNodeId(): string {
    return this.graph.rootId;
  }

  get defaultCapabilitySet(): CapabilitySet {
    return new CapabilitySet(this.defaultCapabilities);
  }

  get viewCount(): number {
    return this.#live.size;
  }

  get pendingRequestCount(): number {
    return this.#pending.size;
  }

  get pendingCount(): number {
    return this.#pending.size;
  }

  /**
   * Open a view of an immutable graph node in a policy context. Capabilities
   * can only be narrowed from the policy node's capability set.
   */
  view(
    nodeIdOrOptions?: string | ViewOptions,
    maybeOptions: ViewOptions = {},
  ): object {
    const { nodeId, options } =
      typeof nodeIdOrOptions === "string"
        ? { nodeId: nodeIdOrOptions, options: maybeOptions }
        : { nodeId: this.graph.rootId, options: nodeIdOrOptions ?? maybeOptions };
    const caps = this.#requestedCaps(options.capabilities);
    const policyNode = this.#selectPolicyNode(options, caps);
    return this.#open(this.graph.has(nodeId) ? nodeId : this.#assertNode(nodeId), {
      nodeId: policyNode,
      capsMask: caps,
    }, new Set(), new Set());
  }

  viewIn(
    domain: string,
    nodeIdOrOptions?: string | ViewOptions,
    maybeOptions: ViewOptions = {},
  ): object {
    const nodeId =
      typeof nodeIdOrOptions === "string" ? nodeIdOrOptions : this.graph.rootId;
    const options =
      typeof nodeIdOrOptions === "string"
        ? maybeOptions
        : nodeIdOrOptions ?? maybeOptions;
    const caps = this.#requestedCaps(options.capabilities);
    const policyNode = options.policyNode ?? this.#uniqueNodeInDomain(domain, caps);
    const selected = this.policy.node(policyNode);
    if (selected.domain !== domain) {
      throw new PolicyError(
        `Policy node "${selected.id}" belongs to "${selected.domain}", not "${domain}"`,
      );
    }
    return this.view(nodeId, { ...options, policyNode });
  }

  rootView(options: ViewOptions = {}): object {
    return this.view(this.graph.rootId, options);
  }

  getView(nodeIdOrOptions?: string | ViewOptions, options: ViewOptions = {}): object {
    return this.view(nodeIdOrOptions, options);
  }

  openView(nodeIdOrOptions?: string | ViewOptions, options: ViewOptions = {}): object {
    return this.view(nodeIdOrOptions, options);
  }

  viewAt(
    domain: string,
    nodeIdOrOptions?: string | ViewOptions,
    options: ViewOptions = {},
  ): object {
    return this.viewIn(domain, nodeIdOrOptions, options);
  }

  open(nodeIdOrOptions?: string | ViewOptions, options: ViewOptions = {}): object {
    return this.view(nodeIdOrOptions, options);
  }

  /**
   * Traverse one authorization/transfer edge. The second argument is either an
   * edge id or a target domain. Capabilities are intersected at every hop.
   */
  pass(view: object, edgeOrDomain: string, domainOrEdge?: string): object {
    const record = this.#record(view);
    this.#ensureLive(record, "pass");
    const edge = this.#resolveEdge(record, edgeOrDomain, domainOrEdge);
    return this.#traverse(record, edge);
  }

  transfer(view: object, edgeOrDomain: string, domainOrEdge?: string): object {
    return this.pass(view, edgeOrDomain, domainOrEdge);
  }

  passTo(view: object, domain: string, edgeId?: string): object {
    return this.pass(view, domain, edgeId);
  }

  forward(view: object, edgeOrDomain: string, domainOrEdge?: string): object {
    return this.pass(view, edgeOrDomain, domainOrEdge);
  }

  /** Attenuate to at most this capability set. Asking for more is a no-op. */
  attenuate(view: object, capabilities: CapabilityInput): object {
    const record = this.#record(view);
    this.#ensureLive(record, "attenuate");
    const caps = record.capsMask & toMask(capabilities);
    if (caps === record.capsMask) return record.proxy;
    return this.#open(
      record.nodeId,
      { nodeId: record.config.nodeId, capsMask: caps },
      cloneSet(record.support),
      cloneSet(record.trace),
      record.anchors,
    );
  }

  /**
   * Re-wrap a view without unwrapping it. The only supported capability
   * operation is meet; a larger requested set cannot widen the view.
   */
  rewrap(view: object, options: RewrapOptions = {}): object {
    const record = this.#record(view);
    this.#ensureLive(record, "rewrap");
    const requested = options.capabilities;
    const caps =
      record.capsMask & (requested === undefined ? record.capsMask : toMask(requested));

    if (options.edgeId !== undefined) {
      const viaEdge = this.#traverse(record, this.#resolveEdge(record, options.edgeId, options.domain));
      if (caps === this.#record(viaEdge).capsMask) return viaEdge;
      return this.attenuate(viaEdge, caps);
    }

    if (options.policyNode !== undefined) {
      const targetNode = this.policy.node(options.policyNode);
      if (
        options.domain !== undefined &&
        targetNode.domain !== options.domain
      ) {
        throw new PolicyError(
          `Policy node "${targetNode.id}" belongs to "${targetNode.domain}", not "${options.domain}"`,
        );
      }
      const targetConfig = {
        nodeId: targetNode.id,
        capsMask: caps & targetNode.capabilities,
      };
      if (
        targetConfig.nodeId !== record.config.nodeId &&
        this.policy.classOf(targetConfig) !== this.policy.classOf(record.config)
      ) {
        throw new PolicyError(
          "Cannot rewrap onto a non-bisimilar policy state; use pass() with an edge",
        );
      }
      return this.#open(
        record.nodeId,
        targetConfig,
        cloneSet(record.support),
        cloneSet(record.trace),
        record.anchors,
      );
    }

    if (options.domain !== undefined && options.domain !== this.policy.configDomain(record.config)) {
      const viaNode = this.#bisimilarNodeInDomain(record, options.domain);
      if (viaNode !== null) {
        return this.#open(
          record.nodeId,
          { nodeId: viaNode.id, capsMask: caps & viaNode.capabilities },
          cloneSet(record.support),
          cloneSet(record.trace),
          record.anchors,
        );
      }
      const edge = this.policy.resolveEdge(record.config, options.domain);
      const target = this.#traverse(record, edge);
      return caps === this.#record(target).capsMask
        ? target
        : this.attenuate(target, caps);
    }

    if (caps === record.capsMask) return record.proxy;
    return this.attenuate(record.proxy, caps);
  }

  capabilitiesOf(view: object): CapabilitySet {
    return new CapabilitySet(this.#record(view).capsMask);
  }

  capsOf(view: object): CapabilitySet {
    return this.capabilitiesOf(view);
  }

  domainOf(view: object): string {
    const record = this.#record(view);
    return this.policy.configDomain(record.config);
  }

  policyNodeOf(view: object): string {
    return this.#record(view).config.nodeId;
  }

  nodeIdOf(view: object): string {
    return this.#record(view).nodeId;
  }

  isRevoked(view: object): boolean {
    return this.#record(view).revoked;
  }

  isView(value: unknown): boolean {
    return this.#recordOrUndefined(value) !== undefined;
  }

  identityOf(view: object): number {
    return this.#semanticIdentityToken(this.#record(view));
  }

  /**
   * Two views share a cache identity when they denote the same underlying
   * description node at the same capability level and their policy
   * configurations are strictly bisimilar. Support/lease identity is
   * deliberately excluded so independent grants still share cached identity.
   */
  isSameIdentity(left: object, right: object): boolean {
    const a = this.#recordOrUndefined(left);
    const b = this.#recordOrUndefined(right);
    if (a === undefined || b === undefined) return false;
    if (left === right) return true;
    if (a.revoked || b.revoked) return false;
    if (a.nodeId !== b.nodeId || a.capsMask !== b.capsMask) return false;
    const leftClass = this.policy.classOf({
      nodeId: a.config.nodeId,
      capsMask: a.capsMask,
    });
    const rightClass = this.policy.classOf({
      nodeId: b.config.nodeId,
      capsMask: b.capsMask,
    });
    return leftClass === rightClass;
  }

  sameIdentity(left: object, right: object): boolean {
    return this.isSameIdentity(left, right);
  }

  sharesIdentity(left: object, right: object): boolean {
    return this.isSameIdentity(left, right);
  }

  isSameLease(left: object, right: object): boolean {
    const a = this.#recordOrUndefined(left);
    const b = this.#recordOrUndefined(right);
    return a !== undefined && a === b;
  }

  describe(view: object): ViewSnapshot {
    return this.#snapshotRecord(this.#record(view));
  }

  /**
   * Revoke one authorization edge. Views whose grant support contains that
   * edge are invalidated or attenuated synchronously. Views on independent
   * branches, even when they share the same bisimulation identity, are left
   * untouched.
   */
  revoke(edgeId: string, options: RevokeOptions = {}): RevocationEvent {
    const edge = this.policy.edge(edgeId);
    if (edge.revoked) throw new PolicyError(`Policy edge "${edgeId}" is already revoked`);
    const action = this.#revocationAction(edge, options);
    this.policy.revoke(edgeId);

    const invalidated: ViewSnapshot[] = [];
    const downgraded: ViewSnapshot[] = [];
    const affected = [...this.#live]
      .filter((record) => record.support.has(edgeId))
      .sort((a, b) => a.serial - b.serial);
    for (const record of affected) {
      if (record.revoked) continue;
      if (action.kind === "invalidate") {
        this.#kill(record, edgeId);
        invalidated.push(this.#snapshotRecord(record));
        continue;
      }
      const targetNode = this.policy.node(record.config.nodeId);
      const newCaps =
        record.capsMask & ~action.removeMask & targetNode.capabilities;
      if (newCaps === 0) {
        this.#kill(record, edgeId);
        invalidated.push(this.#snapshotRecord(record));
        continue;
      }
      this.#downgrade(record, newCaps, edgeId);
      downgraded.push(this.#snapshotRecord(record));
    }

    const hidden = new Set<string>();
    for (const node of this.graph.nodes.values()) {
      for (const property of node.properties.values()) {
        if (property.grants.includes(edgeId)) {
          hidden.add(`${node.id}.${property.key}`);
        }
      }
    }
    for (const [nodeId, properties] of this.#overlay) {
      for (const property of properties.values()) {
        if (property.grants.includes(edgeId)) {
          hidden.add(`${nodeId}.${property.key}`);
        }
      }
    }
    const hiddenProperties = [...hidden].sort();

    const event: RevocationEvent = Object.freeze({
      edgeId,
      kind: edge.kind,
      invalidated: Object.freeze(invalidated),
      downgraded: Object.freeze(downgraded),
      hiddenProperties: Object.freeze(hiddenProperties),
    });
    this.#rejectPending(edgeId);
    this.#notify(event);
    return event;
  }

  revokeEdge(edgeId: string, options: RevokeOptions = {}): RevocationEvent {
    return this.revoke(edgeId, options);
  }

  revokeAuthorization(edgeId: string, options: RevokeOptions = {}): RevocationEvent {
    return this.revoke(edgeId, options);
  }

  restore(edgeId: string): void {
    this.policy.restore(edgeId);
  }

  onRevoke(
    listener: (event: RevocationEvent) => void,
    dependencies?: string | Iterable<string>,
  ): () => void {
    const entry: RevocationListener = {
      listener,
      deps:
        dependencies === undefined
          ? null
          : new Set(dependencyList(dependencies)),
    };
    this.#listeners.add(entry);
    return () => {
      this.#listeners.delete(entry);
    };
  }

  subscribeRevocation(
    listener: (event: RevocationEvent) => void,
    dependencies?: string | Iterable<string>,
  ): () => void {
    return this.onRevoke(listener, dependencies);
  }

  onRevoked(
    listener: (event: RevocationEvent) => void,
    dependencies?: string | Iterable<string>,
  ): () => void {
    return this.onRevoke(listener, dependencies);
  }

  /**
   * Asynchronously open a view. If a grant on which the request depends is
   * revoked before the callback settles, the promise rejects instead of
   * delivering a stale view.
   */
  requestView(
    nodeIdOrOptions?: string | RequestOptions,
    maybeOptions: RequestOptions = {},
  ): Promise<object> {
    const nodeId =
      typeof nodeIdOrOptions === "string" ? nodeIdOrOptions : this.graph.rootId;
    const options =
      typeof nodeIdOrOptions === "string"
        ? maybeOptions
        : nodeIdOrOptions ?? maybeOptions;
    const deps = new Set(dependencyList(options.dependencies));
    return this.#schedule(deps, options, () => this.view(nodeId, options));
  }

  asyncView(
    nodeIdOrOptions?: string | RequestOptions,
    options: RequestOptions = {},
  ): Promise<object> {
    return this.requestView(nodeIdOrOptions, options);
  }

  asyncPass(
    view: object,
    edgeOrDomain: string,
    options: (RequestOptions & { domainOrEdge?: string }) | string = {},
  ): Promise<object> {
    return this.requestPass(view, edgeOrDomain, options);
  }

  requestPass(
    view: object,
    edgeOrDomain: string,
    options: (RequestOptions & { domainOrEdge?: string }) | string = {},
  ): Promise<object> {
    try {
      const opts: RequestOptions & { domainOrEdge?: string } =
        typeof options === "string" ? { domainOrEdge: options } : options;
      const record = this.#record(view);
      this.#ensureLive(record, "requestPass");
      const edge = this.#resolveEdge(record, edgeOrDomain, opts.domainOrEdge);
      const deps = cloneSet(record.support);
      if (edge.grant) deps.add(edge.id);
      for (const dependency of dependencyList(opts.dependencies)) deps.add(dependency);
      return this.#schedule(deps, opts, () => {
        const current = this.#record(view);
        this.#ensureLive(current, "requestPass");
        return this.#traverse(current, this.policy.edge(edge.id));
      });
    } catch (error) {
      return Promise.reject(error);
    }
  }

  /**
   * Symbolic asynchronous invocation. No carried function is executed; the
   * call descriptor is evaluated after the scheduling boundary. Revocation
   * before that boundary rejects the promise.
   */
  requestInvoke(
    view: object,
    args: readonly unknown[] = [],
    options: RequestOptions = {},
  ): Promise<unknown> {
    try {
      const record = this.#record(view);
      this.#ensureLive(record, "requestInvoke");
      const deps = cloneSet(record.support);
      for (const dependency of dependencyList(options.dependencies)) deps.add(dependency);
      return this.#schedule(deps, options, () => {
        const current = this.#record(view);
        this.#ensureLive(current, "requestInvoke");
        return this.#invoke(current, args, true);
      });
    } catch (error) {
      return Promise.reject(error);
    }
  }

  asyncInvoke(
    view: object,
    args: readonly unknown[] = [],
    options: RequestOptions = {},
  ): Promise<unknown> {
    return this.requestInvoke(view, args, options);
  }

  invoke(view: object, args: readonly unknown[] = []): unknown {
    const record = this.#record(view);
    this.#ensureLive(record, "invoke");
    return this.#invoke(record, args, false);
  }

  call(view: object, args: readonly unknown[] = []): unknown {
    return this.invoke(view, args);
  }

  construct(view: object, args: readonly unknown[] = []): object {
    const record = this.#record(view);
    this.#ensureLive(record, "construct");
    return this.#construct(record, args);
  }

  policyModel(): FiniteStateModel {
    return this.policy.model();
  }

  finiteStateModel(): FiniteStateModel {
    return this.policy.model();
  }

  model(): FiniteStateModel {
    return this.policy.model();
  }

  snapshot(): HostSnapshot {
    const policyNodeSnapshots = [...this.policy.nodes.values()].map((node) => ({
      id: node.id,
      domain: node.domain,
      capabilities: [...new CapabilitySet(node.capabilities)],
    }));
    const edgeSnapshots = [...this.policy.edges.values()].map((edge) => ({
      id: edge.id,
      from: edge.from,
      to: edge.to,
      kind: edge.kind,
      revoked: edge.revoked,
    }));
    const views = [...this.#live]
      .sort((a, b) => a.serial - b.serial)
      .map((record) => this.#snapshotRecord(record));
    let identities = 0;
    let leases = 0;
    for (const byNode of this.#identities.values()) {
      for (const byCaps of byNode.values()) {
        for (const identity of byCaps.values()) {
          if (identity.liveCount > 0) {
            identities++;
            leases += identity.leases.size;
          }
        }
      }
    }
    return {
      rootId: this.graph.rootId,
      defaultDomain: this.defaultDomain,
      defaultPolicyNode: this.defaultPolicyNode,
      policy: {
        entry: this.policy.entry,
        classCount: this.policy.classCount,
        configurationCount: this.policy.configStateCount,
        iterationCount: this.policy.iterationCount,
        nodes: policyNodeSnapshots,
        edges: edgeSnapshots,
      },
      views,
      cache: { identities, leases },
      pendingRequests: this.#pending.size,
    };
  }

  #assertNode(nodeId: string): string {
    this.graph.get(nodeId);
    return nodeId;
  }

  #requestedCaps(input: CapabilityInput | undefined): number {
    if (input === undefined) return this.defaultCapabilities;
    return toMask(input);
  }

  #uniqueNodeInDomain(domain: string, capabilities: number = this.defaultCapabilities): string {
    const candidates = this.policy.nodesInDomain(domain);
    if (candidates.length === 1) return candidates[0].id;
    if (candidates.length === 0) {
      throw new PolicyError(`No policy node belongs to domain "${domain}"`);
    }
    const firstClass = this.policy.classOf({
      nodeId: candidates[0].id,
      capsMask: capabilities & candidates[0].capabilities,
    });
    if (
      candidates.every(
        (node) =>
          this.policy.classOf({
            nodeId: node.id,
            capsMask: capabilities & node.capabilities,
          }) === firstClass,
      )
    ) {
      return candidates[0].id;
    }
    const entry = this.policy.node(this.policy.entry);
    if (entry.domain === domain) return entry.id;
    throw new PolicyError(`Domain "${domain}" has multiple policy nodes; specify policyNode`);
  }

  #selectPolicyNode(options: ViewOptions, capabilities: number): string {
    if (options.policyNode !== undefined) {
      const node = this.policy.node(options.policyNode);
      if (options.domain !== undefined && node.domain !== options.domain) {
        throw new PolicyError(
          `Policy node "${node.id}" belongs to "${node.domain}", not "${options.domain}"`,
        );
      }
      return node.id;
    }
    if (options.domain !== undefined) {
      return this.#uniqueNodeInDomain(options.domain, capabilities);
    }
    return this.defaultPolicyNode;
  }

  #bisimilarNodeInDomain(
    record: ViewRecord,
    domain: string,
  ): { id: string; capabilities: number } | null {
    const currentClass = this.policy.classOf(record.config);
    for (const node of this.policy.nodesInDomain(domain)) {
      const caps = record.capsMask & node.capabilities;
      const cls = this.policy.classOf({ nodeId: node.id, capsMask: caps });
      if (cls === currentClass) return { id: node.id, capabilities: node.capabilities };
    }
    return null;
  }

  #resolveEdge(
    record: ViewRecord,
    edgeOrDomain: string,
    domainOrEdge?: string,
  ): PolicyEdgeDef {
    if (domainOrEdge !== undefined) {
      const firstIsEdge = this.policy.edges.has(edgeOrDomain);
      const edgeId = firstIsEdge ? edgeOrDomain : domainOrEdge;
      const domain = firstIsEdge ? domainOrEdge : edgeOrDomain;
      return this.policy.resolveEdge(record.config, edgeId, domain);
    }
    return this.policy.resolveEdge(record.config, edgeOrDomain);
  }

  #traverse(record: ViewRecord, edge: PolicyEdgeDef): object {
    const nextConfig = this.policy.traverse(record.config, edge.id);
    const support = cloneSet(record.support);
    if (edge.grant) support.add(edge.id);
    const trace = cloneSet(record.trace);
    trace.add(edge.id);
    return this.#open(record.nodeId, nextConfig, support, trace, record.anchors);
  }

  #record(view: object): ViewRecord {
    const record = this.#recordOrUndefined(view);
    if (record === undefined) {
      throw new PolicyError("Expected a proxy view created by this host");
    }
    return record;
  }

  #recordOrUndefined(value: unknown): ViewRecord | undefined {
    if (
      (typeof value !== "object" && typeof value !== "function") ||
      value === null
    ) {
      return undefined;
    }
    const direct = this.#records.get(value);
    if (direct !== undefined) return direct;
    try {
      const candidate = (value as Record<PropertyKey, unknown>)[VIEW_RECORD_BRAND];
      if (candidate === null || typeof candidate !== "object") return undefined;
      return this.#brandTokens.get(candidate);
    } catch {
      return undefined;
    }
    return undefined;
  }

  #ensureLive(record: ViewRecord, operation: string): void {
    if (record.revoked) {
      throw new RevokedViewError(
        `Cannot ${operation} through a revoked proxy view`,
        record.revokedBy,
      );
    }
  }

  #identityFor(
    nodeId: string,
    capsMask: number,
    policyClass: BisimulationClass,
  ): IdentityRecord {
    let byNode = this.#identities.get(policyClass);
    if (byNode === undefined) {
      byNode = new Map();
      this.#identities.set(policyClass, byNode);
    }
    let byCaps = byNode.get(nodeId);
    if (byCaps === undefined) {
      byCaps = new Map();
      byNode.set(nodeId, byCaps);
    }
    let identity = byCaps.get(capsMask);
    if (identity === undefined) {
      identity = {
        nodeId,
        capsMask,
        policyClass,
        leases: new Map(),
        liveCount: 0,
      };
      byCaps.set(capsMask, identity);
    }
    return identity;
  }

  #semanticIdentityToken(record: ViewRecord): number {
    const policyClass = this.policy.classOf({
      nodeId: record.config.nodeId,
      capsMask: record.capsMask,
    });
    let byNode = this.#semanticIdentityTokens.get(policyClass);
    if (byNode === undefined) {
      byNode = new Map();
      this.#semanticIdentityTokens.set(policyClass, byNode);
    }
    let byCaps = byNode.get(record.nodeId);
    if (byCaps === undefined) {
      byCaps = new Map();
      byNode.set(record.nodeId, byCaps);
    }
    let token = byCaps.get(record.capsMask);
    if (token === undefined) {
      token = this.#nextSemanticIdentityToken++;
      byCaps.set(record.capsMask, token);
    }
    return token;
  }

  #open(
    nodeId: string,
    config: PolicyConfig,
    support: Set<string>,
    trace: Set<string>,
    anchors: readonly ViewRecord[] = [],
  ): object {
    const node = this.graph.get(nodeId);
    const targetNode = this.policy.node(config.nodeId);
    const caps = config.capsMask & targetNode.capabilities;
    const normalizedConfig: PolicyConfig = Object.freeze({
      nodeId: config.nodeId,
      capsMask: caps,
    });
    const policyClass = this.policy.classOf(normalizedConfig);
    for (let index = anchors.length - 1; index >= 0; index--) {
      const anchor = anchors[index];
      if (anchor.revoked) continue;
      if (anchor.nodeId !== nodeId || anchor.capsMask !== caps) continue;
      const anchorClass = this.policy.classOf(anchor.config);
      if (anchorClass === policyClass) return anchor.proxy;
    }
    const identity = this.#identityFor(nodeId, caps, policyClass);
    const key = supportKey(support);
    const existing = identity.leases.get(key);
    if (existing !== undefined && !existing.revoked) {
      return existing.proxy;
    }
    for (const candidate of this.#live) {
      if (
        candidate.nodeId !== nodeId ||
        candidate.capsMask !== caps ||
        candidate.leaseKey !== key ||
        candidate.revoked
      ) {
        continue;
      }
      if (this.policy.classOf(candidate.config) === policyClass) {
        return candidate.proxy;
      }
    }

    const target = createProxyTarget(node.kind);
    const record: ViewRecord = {
      brand: true,
      brandToken: Object.freeze({}),
      serial: this.#nextViewSerial++,
      proxy: null as unknown as object,
      target,
      nodeId,
      config: normalizedConfig,
      capsMask: caps,
      support: cloneSet(support),
      trace: cloneSet(trace),
      anchors: [],
      identity,
      leaseKey: key,
      revoked: false,
      revokedBy: null,
      epoch: 0,
    };

    const handler: ProxyHandler<object> = {
      get: (_target, property) => this.#get(record, property),
      set: (_target, property, value) => this.#set(record, property, value),
      has: (_target, property) => this.#has(record, property),
      ownKeys: () => this.#ownKeys(record),
      getOwnPropertyDescriptor: (_target, property) =>
        this.#descriptor(record, property),
      defineProperty: (_target, property, descriptor) =>
        this.#defineProperty(record, property, descriptor),
      deleteProperty: (_target, property) => this.#deleteProperty(record, property),
      getPrototypeOf: () => this.#prototype(record),
      setPrototypeOf: () => false,
      isExtensible: () => true,
      preventExtensions: () => false,
      apply: (_target, _thisArg, args) => this.#invoke(record, args, false),
      construct: (_target, args) => this.#construct(record, args),
    };
    const proxy = new Proxy(target, handler);
    record.proxy = proxy;
    record.anchors = Object.freeze([...anchors, record]);
    this.#records.set(proxy, record);
    this.#brandTokens.set(record.brandToken, record);
    this.#live.add(record);
    identity.leases.set(key, record);
    identity.liveCount++;
    return proxy;
  }

  #snapshotRecord(record: ViewRecord): ViewSnapshot {
    return {
      id: record.serial,
      nodeId: record.nodeId,
      domain: this.policy.configDomain(record.config),
      policyNode: record.config.nodeId,
      capabilities: [...new CapabilitySet(record.capsMask)],
      support: [...record.support].sort(),
      trace: [...record.trace].sort(),
      identity: this.#semanticIdentityToken(record),
      revoked: record.revoked,
      revokedBy: record.revokedBy,
    };
  }

  #detachIdentity(record: ViewRecord): void {
    if (record.identity.leases.get(record.leaseKey) === record) {
      record.identity.leases.delete(record.leaseKey);
    }
    record.identity.liveCount--;
    if (record.identity.liveCount <= 0) {
      const byNode = this.#identities.get(record.identity.policyClass);
      const byCaps = byNode?.get(record.identity.nodeId);
      if (byCaps?.get(record.identity.capsMask) === record.identity) {
        byCaps.delete(record.identity.capsMask);
        if (byCaps.size === 0) byNode?.delete(record.identity.nodeId);
        if (byNode?.size === 0) this.#identities.delete(record.identity.policyClass);
      }
    }
  }

  #attachIdentity(record: ViewRecord): void {
    const policyClass = this.policy.classOf(record.config);
    const identity = this.#identityFor(record.nodeId, record.capsMask, policyClass);
    record.identity = identity;
    record.leaseKey = supportKey(record.support);
    if (!identity.leases.has(record.leaseKey)) {
      identity.leases.set(record.leaseKey, record);
    }
    identity.liveCount++;
  }

  #downgrade(record: ViewRecord, newCaps: number, edgeId: string): void {
    this.#detachIdentity(record);
    record.config = Object.freeze({ nodeId: record.config.nodeId, capsMask: newCaps });
    record.capsMask = newCaps;
    record.support.delete(edgeId);
    record.trace.delete(edgeId);
    record.epoch++;
    this.#attachIdentity(record);
  }

  #kill(record: ViewRecord, edgeId: string): void {
    if (record.revoked) return;
    this.#detachIdentity(record);
    record.revoked = true;
    record.revokedBy = edgeId;
    record.epoch++;
    this.#live.delete(record);
  }

  #revocationAction(edge: PolicyEdgeDef, options: RevokeOptions): RevocationAction {
    if (options.invalidate === true) return { kind: "invalidate" };
    const remove =
      (options.attenuate === undefined ? 0 : toMask(options.attenuate)) |
      (options.deny === undefined ? 0 : toMask(options.deny));
    if (options.retain !== undefined) {
      return {
        kind: "attenuate",
        removeMask: ALL_CAPABILITY_MASK & ~toMask(options.retain),
      };
    }
    if (remove !== 0) return { kind: "attenuate", removeMask: remove };
    return edge.onRevoke;
  }

  #notify(event: RevocationEvent): void {
    for (const entry of this.#listeners) {
      if (entry.deps !== null && !entry.deps.has(event.edgeId)) continue;
      try {
        entry.listener(event);
      } catch {
        // Revocation must complete even if an observer fails.
      }
    }
  }

  #rejectPending(edgeId: string): void {
    for (const pending of [...this.#pending]) {
      if (!pending.deps.has(edgeId)) continue;
      if (pending.settled) continue;
      pending.settled = true;
      if (pending.timer !== null) clearTimeout(pending.timer);
      this.#pending.delete(pending);
      pending.reject(
        new RevokedViewError(
          `Async result depended on revoked authorization edge "${edgeId}"`,
          edgeId,
        ),
      );
    }
  }

  #schedule<T>(
    deps: ReadonlySet<string>,
    options: { delayMs?: number; microtask?: boolean },
    settle: () => T,
  ): Promise<T> {
    try {
      for (const edgeId of deps) {
        if (this.policy.edge(edgeId).revoked) {
          return Promise.reject(
            new RevokedViewError(
              `Async result depends on revoked authorization edge "${edgeId}"`,
              edgeId,
            ),
          );
        }
      }
    } catch (error) {
      return Promise.reject(error);
    }
    return new Promise<T>((resolve, reject) => {
      const pending: PendingRequest = {
        id: this.#nextRequestId++,
        deps,
        settle,
        resolve: resolve as (value: unknown) => void,
        reject,
        timer: null,
        settled: false,
      };
      this.#pending.add(pending);
      const run = (): void => {
        if (pending.settled) return;
        pending.settled = true;
        this.#pending.delete(pending);
        for (const edgeId of deps) {
          if (this.policy.edge(edgeId).revoked) {
            reject(
              new RevokedViewError(
                `Async result depended on revoked authorization edge "${edgeId}"`,
                edgeId,
              ),
            );
            return;
          }
        }
        try {
          pending.resolve(settle());
        } catch (error) {
          reject(error);
        }
      };
      if (options.microtask === true && (options.delayMs ?? 0) === 0) {
        queueMicrotask(run);
      } else {
        pending.timer = setTimeout(run, options.delayMs ?? 0);
      }
    });
  }

  #lookupOwn(nodeId: string, key: string): OverlayProperty | undefined {
    const overlay = this.#overlay.get(nodeId)?.get(key);
    if (overlay !== undefined) return overlay;
    if (this.#deleted.get(nodeId)?.has(key)) return undefined;
    const property = this.graph.get(nodeId).properties.get(key);
    return property === undefined ? undefined : fromGraphProperty(property);
  }

  #lookup(record: ViewRecord, key: string): ResolvedProperty | null {
    let nodeId = record.nodeId;
    let chainCaps = record.capsMask;
    let chainRequires = 0;
    const chainGrants: string[] = [];
    const seen = new Set<string>();
    for (;;) {
      if (seen.has(nodeId)) return null;
      seen.add(nodeId);
      const own = this.#lookupOwn(nodeId, key);
      if (own !== undefined) {
        return {
          prop: own,
          owner: nodeId,
          chainCaps,
          chainRequires: chainRequires | own.requires,
          chainGrants: Object.freeze([...chainGrants, ...own.grants]),
        };
      }
      const node = this.graph.get(nodeId);
      const link = node.prototype;
      if (link === null) return null;
      if ((link.requires & ~chainCaps) !== 0) return null;
      if (!this.#grantsActive(link.grants)) return null;
      chainRequires |= link.requires;
      chainGrants.push(...link.grants);
      chainCaps &= link.capabilities;
      nodeId = link.ref;
    }
  }

  #grantsActive(grants: readonly string[]): boolean {
    for (const grant of grants) {
      if (this.policy.edge(grant).revoked) return false;
    }
    return true;
  }

  #propertyVisible(record: ViewRecord, resolved: ResolvedProperty): boolean {
    const { prop, chainCaps, chainRequires, chainGrants } = resolved;
    if (((prop.requires | chainRequires) & ~chainCaps) !== 0) return false;
    if (!this.#grantsActive(chainGrants)) return false;
    return true;
  }

  #get(record: ViewRecord, property: string | symbol): unknown {
    if (property === VIEW_RECORD_BRAND) return record.brandToken;
    this.#ensureLive(record, "get");
    if (typeof property === "symbol") return this.#symbol(record, property);
    if (property === "then") return undefined;
    requireCapability(record.capsMask, "read", `property "${property}"`);
    const resolved = this.#lookup(record, property);
    if (resolved === null || !this.#propertyVisible(record, resolved)) return undefined;
    return this.#materialize(record, resolved.prop, resolved.chainCaps);
  }

  #symbol(record: ViewRecord, symbol: symbol): unknown {
    if (symbol === Symbol.toStringTag) {
      return hasCapability(record.capsMask, "read") ? VIEW_TAG : undefined;
    }
    if (symbol === INSPECT_SYMBOL) return undefined;
    return undefined;
  }

  #materialize(
    record: ViewRecord,
    prop: PropertyDef | OverlayProperty,
    chainCaps: number,
  ): unknown {
    if (prop.value.kind === "primitive") return prop.value.value;
    const caps = chainCaps & prop.capabilities;
    return this.#open(
      prop.value.ref,
      { nodeId: record.config.nodeId, capsMask: caps },
      cloneSet(record.support),
      cloneSet(record.trace),
      record.anchors,
    );
  }

  #has(record: ViewRecord, property: string | symbol): boolean {
    this.#ensureLive(record, "has");
    if (typeof property === "symbol") return false;
    if (
      !hasCapability(record.capsMask, "read") &&
      !hasCapability(record.capsMask, "enumerate")
    ) {
      throw new CapabilityError(
        `Capability "read" or "enumerate" is required for the in operator`,
        "read",
      );
    }
    const resolved = this.#lookup(record, property);
    return resolved !== null && this.#propertyVisible(record, resolved);
  }

  #ownKeys(record: ViewRecord): Array<string | symbol> {
    this.#ensureLive(record, "ownKeys");
    if (!hasCapability(record.capsMask, "enumerate")) return [];
    const keys = new Set<string>();
    for (const property of this.graph.get(record.nodeId).properties.values()) {
      keys.add(property.key);
    }
    for (const key of this.#overlay.get(record.nodeId)?.keys() ?? []) {
      keys.add(key);
    }
    for (const key of this.#deleted.get(record.nodeId) ?? []) keys.delete(key);
    const visible: string[] = [];
    for (const key of keys) {
      const prop = this.#lookupOwn(record.nodeId, key);
      if (prop === undefined) continue;
      if (
        !this.#propertyVisible(record, {
          prop,
          owner: record.nodeId,
          chainCaps: record.capsMask,
          chainRequires: prop.requires,
          chainGrants: prop.grants,
        })
      ) {
        continue;
      }
      visible.push(key);
    }
    visible.sort(canonicalKeyCompare);
    return visible;
  }

  #descriptor(
    record: ViewRecord,
    property: string | symbol,
  ): PropertyDescriptor | undefined {
    this.#ensureLive(record, "getOwnPropertyDescriptor");
    if (typeof property === "symbol") return undefined;
    const canRead = hasCapability(record.capsMask, "read");
    const canEnumerate = hasCapability(record.capsMask, "enumerate");
    if (!canRead && !canEnumerate) return undefined;
    const prop = this.#lookupOwn(record.nodeId, property);
    if (prop === undefined) return undefined;
    if (
      !this.#propertyVisible(record, {
        prop,
        owner: record.nodeId,
        chainCaps: record.capsMask,
        chainRequires: prop.requires,
        chainGrants: prop.grants,
      })
    ) {
      return undefined;
    }
    return {
      value: canRead ? this.#materialize(record, prop, record.capsMask) : undefined,
      writable: prop.writable && hasCapability(record.capsMask, "write"),
      enumerable: prop.enumerable,
      configurable: true,
    };
  }

  #normalizeWrite(
    record: ViewRecord,
    value: unknown,
  ): { stored: StoredValue; caps: number; grants: readonly string[] } {
    const source = this.#recordOrUndefined(value);
    if (source !== undefined) {
      if (source.revoked) {
        throw new RevokedViewError("Cannot store a revoked proxy view", source.revokedBy);
      }
      return {
        stored: Object.freeze({ kind: "ref" as const, ref: source.nodeId }),
        caps: record.capsMask & source.capsMask,
        grants: Object.freeze([...source.support]),
      };
    }
    if (typeof value === "function") {
      throw new InvalidWriteError("Raw functions cannot be stored in a described object");
    }
    if (isPrimitive(value)) {
      return {
        stored: Object.freeze({ kind: "primitive" as const, value }),
        caps: ALL_CAPABILITY_MASK,
        grants: Object.freeze([] as string[]),
      };
    }
    if (typeof value === "object" && value !== null) {
      throw new InvalidWriteError(
        "Only primitives or views created by this host may be assigned",
      );
    }
    throw new InvalidWriteError("Unsupported value for a described object");
  }

  #set(record: ViewRecord, property: string | symbol, value: unknown): boolean {
    this.#ensureLive(record, "set");
    if (typeof property === "symbol") return false;
    requireCapability(record.capsMask, "write", `setting "${property}"`);
    const resolved = this.#lookup(record, property);
    if (resolved !== null && !this.#propertyVisible(record, resolved)) return false;
    const own = this.#lookupOwn(record.nodeId, property);
    if (own !== undefined && !own.writable) return false;
    if (resolved !== null && resolved.owner !== record.nodeId && !resolved.prop.writable) {
      return false;
    }
    const normalized = this.#normalizeWrite(record, value);
    const base = own;
    const chainCaps = resolved?.chainCaps ?? record.capsMask;
    const baseCaps =
      own?.capabilities ?? resolved?.prop.capabilities ?? chainCaps;
    const requires = own?.requires ?? resolved?.chainRequires ?? 0;
    const existingGrants = own?.grants ?? resolved?.chainGrants;
    const grants =
      existingGrants !== undefined
        ? Object.freeze([...new Set(existingGrants)])
        : Object.freeze([
            ...new Set([...record.support, ...normalized.grants]),
          ]);
    const overlay: OverlayProperty = {
      key: property,
      value: normalized.stored,
      writable: true,
      enumerable: base?.enumerable ?? true,
      configurable: true,
      capabilities:
        normalized.stored.kind === "ref"
          ? baseCaps & chainCaps & normalized.caps
          : baseCaps,
      requires,
      grants,
      order: base?.order ?? this.#nextOrder++,
    };
    this.#putOverlay(record.nodeId, overlay);
    this.#deleted.get(record.nodeId)?.delete(property);
    return true;
  }

  #defineProperty(
    record: ViewRecord,
    property: string | symbol,
    descriptor: PropertyDescriptor,
  ): boolean {
    this.#ensureLive(record, "defineProperty");
    if (typeof property === "symbol") return false;
    requireCapability(record.capsMask, "write", `defining "${property}"`);
    if (descriptor.configurable === false) return false;
    if ("get" in descriptor || "set" in descriptor) {
      throw new InvalidWriteError("Accessor descriptors would carry executable functions");
    }
    const own = this.#lookupOwn(record.nodeId, property);
    if (own !== undefined && !own.writable) return false;
    const resolved = this.#lookup(record, property);
    if (resolved !== null && !this.#propertyVisible(record, resolved)) return false;
    const hasValue = Object.prototype.hasOwnProperty.call(descriptor, "value");
    const normalized = hasValue
      ? this.#normalizeWrite(record, descriptor.value)
      : {
          stored:
            own?.value ??
            Object.freeze({ kind: "primitive" as const, value: undefined }),
          caps: own?.capabilities ?? ALL_CAPABILITY_MASK,
          grants: Object.freeze([] as string[]),
        };
    const existingGrants = own?.grants ?? resolved?.chainGrants;
    const baseCaps =
      own?.capabilities ??
      resolved?.prop.capabilities ??
      resolved?.chainCaps ??
      record.capsMask;
    const overlay: OverlayProperty = {
      key: property,
      value: normalized.stored,
      writable: descriptor.writable ?? own?.writable ?? false,
      enumerable: descriptor.enumerable ?? own?.enumerable ?? false,
      configurable: true,
      capabilities:
        normalized.stored.kind === "ref"
          ? normalized.caps &
            (resolved?.chainCaps ?? record.capsMask) &
            baseCaps
          : baseCaps,
      requires: own?.requires ?? resolved?.chainRequires ?? 0,
      grants:
        existingGrants !== undefined
          ? Object.freeze([...new Set(existingGrants)])
          : Object.freeze([
              ...new Set([...record.support, ...normalized.grants]),
            ]),
      order: own?.order ?? this.#nextOrder++,
    };
    this.#putOverlay(record.nodeId, overlay);
    this.#deleted.get(record.nodeId)?.delete(property);
    return true;
  }

  #deleteProperty(record: ViewRecord, property: string | symbol): boolean {
    this.#ensureLive(record, "deleteProperty");
    if (typeof property === "symbol") return false;
    requireCapability(record.capsMask, "write", `deleting "${property}"`);
    const own = this.#lookupOwn(record.nodeId, property);
    if (own === undefined) return true;
    if (
      !this.#propertyVisible(record, {
        prop: own,
        owner: record.nodeId,
        chainCaps: record.capsMask,
        chainRequires: own.requires,
        chainGrants: own.grants,
      })
    ) {
      return false;
    }
    if (!own.configurable) return false;
    this.#overlay.get(record.nodeId)?.delete(property);
    let deleted = this.#deleted.get(record.nodeId);
    if (deleted === undefined) {
      deleted = new Set();
      this.#deleted.set(record.nodeId, deleted);
    }
    deleted.add(property);
    return true;
  }

  #putOverlay(nodeId: string, property: OverlayProperty): void {
    let map = this.#overlay.get(nodeId);
    if (map === undefined) {
      map = new Map();
      this.#overlay.set(nodeId, map);
    }
    map.set(property.key, property);
  }

  #prototype(record: ViewRecord): object | null {
    this.#ensureLive(record, "getPrototypeOf");
    if (!hasCapability(record.capsMask, "read")) return null;
    const node = this.graph.get(record.nodeId);
    const link = node.prototype;
    if (link === null) return null;
    if ((link.requires & ~record.capsMask) !== 0) return null;
    if (!this.#grantsActive(link.grants)) return null;
    return this.#open(
      link.ref,
      {
        nodeId: record.config.nodeId,
        capsMask: record.capsMask & link.capabilities,
      },
      cloneSet(record.support),
      cloneSet(record.trace),
      record.anchors,
    );
  }

  #callNode(record: ViewRecord, mode: "invoke" | "construct"): {
    node: NodeDef;
    call: CallDef;
  } {
    requireCapability(record.capsMask, mode, mode);
    const node = this.graph.get(record.nodeId);
    if (node.kind !== "function") {
      throw new SymbolicOperationError(`Node "${record.nodeId}" is not a function`);
    }
    const call = mode === "invoke" ? node.invoke : node.construct;
    if (call === null) {
      throw new SymbolicOperationError(
        `Node "${record.nodeId}" has no symbolic ${mode} descriptor`,
      );
    }
    if (call.throws !== null) {
      throw new SymbolicOperationError(call.throws);
    }
    return { node, call };
  }

  #invoke(record: ViewRecord, _args: readonly unknown[], fromAsync: boolean): unknown {
    this.#ensureLive(record, fromAsync ? "async invoke" : "invoke");
    const { call } = this.#callNode(record, "invoke");
    const caps = record.capsMask & call.capabilities;
    if (call.result.kind === "primitive") return call.result.value;
    return this.#open(
      call.result.ref,
      { nodeId: record.config.nodeId, capsMask: caps },
      cloneSet(record.support),
      cloneSet(record.trace),
      record.anchors,
    );
  }

  #construct(record: ViewRecord, _args: readonly unknown[]): object {
    this.#ensureLive(record, "construct");
    const { call } = this.#callNode(record, "construct");
    if (call.result.kind !== "ref") {
      throw new SymbolicOperationError(
        "A construct descriptor must return an object node reference",
      );
    }
    const caps = record.capsMask & call.capabilities;
    return this.#open(
      call.result.ref,
      { nodeId: record.config.nodeId, capsMask: caps },
      cloneSet(record.support),
      cloneSet(record.trace),
      record.anchors,
    );
  }
}

export function createHost(options: HostOptions): MultiDomainViewHost {
  return new MultiDomainViewHost(options);
}

export function createBisimulationHost(options: HostOptions): MultiDomainViewHost {
  return new MultiDomainViewHost(options);
}

export function createViewHost(options: HostOptions): MultiDomainViewHost {
  return new MultiDomainViewHost(options);
}

export function createMultiDomainViewHost(options: HostOptions): MultiDomainViewHost {
  return new MultiDomainViewHost(options);
}

export function createBisimulationViewHost(options: HostOptions): MultiDomainViewHost {
  return new MultiDomainViewHost(options);
}

export function createMultiDomainBisimulationHost(
  options: HostOptions,
): MultiDomainViewHost {
  return new MultiDomainViewHost(options);
}
