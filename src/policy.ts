import {
  ALL_CAPABILITY_MASK,
  toMask,
  type CapabilityInput,
} from "./capabilities.ts";
import {
  ClassInterner,
  FiniteStateModel,
  computeBisimulation,
  type BisimulationClass,
  type BisimulationResult,
  type LabelledState,
} from "./bisim.ts";
import { InvalidDescriptionError, PolicyError } from "./errors.ts";
import { FrozenMap } from "./immutable.ts";

export type PolicyEdgeKind = "transfer" | "grant";

export interface PolicyNodeSpec {
  readonly id?: string;
  readonly domain: string;
  readonly capabilities?: CapabilityInput;
  readonly caps?: CapabilityInput;
}

export interface RevocationSpec {
  readonly attenuate?: CapabilityInput;
  readonly deny?: CapabilityInput;
  readonly retain?: CapabilityInput;
}

export interface PolicyEdgeSpec {
  readonly id?: string;
  readonly from: string;
  readonly to: string;
  readonly kind?: PolicyEdgeKind;
  readonly grant?: boolean;
  readonly authorization?: boolean;
  readonly revocable?: boolean;
  readonly action?: string;
  readonly capabilities?: CapabilityInput;
  readonly caps?: CapabilityInput;
  readonly attenuate?: CapabilityInput;
  readonly deny?: CapabilityInput;
  readonly onRevoke?: "invalidate" | "attenuate" | "deny" | boolean | RevocationSpec;
  readonly revoked?: boolean;
}

export interface PolicySpec {
  readonly entry?: string;
  readonly start?: string;
  readonly nodes?: readonly PolicyNodeSpec[] | Readonly<Record<string, PolicyNodeSpec>>;
  readonly principals?: readonly PolicyNodeSpec[] | Readonly<Record<string, PolicyNodeSpec>>;
  readonly edges?:
    | readonly PolicyEdgeSpec[]
    | Readonly<Record<string, PolicyEdgeSpec>>;
  readonly grants?:
    | readonly PolicyEdgeSpec[]
    | Readonly<Record<string, PolicyEdgeSpec>>;
}

export interface PolicyNodeDef {
  readonly id: string;
  readonly domain: string;
  readonly capabilities: number;
}

export type RevocationAction =
  | { readonly kind: "invalidate" }
  | { readonly kind: "attenuate"; readonly removeMask: number };

export interface PolicyEdgeDef {
  readonly id: string;
  readonly from: string;
  readonly to: string;
  readonly kind: PolicyEdgeKind;
  readonly action: string;
  readonly capabilities: number;
  readonly grant: boolean;
  readonly onRevoke: RevocationAction;
  readonly index: number;
  revoked: boolean;
}

export interface PolicyConfig {
  readonly nodeId: string;
  readonly capsMask: number;
}

function readOwn(value: object, key: string): unknown {
  const descriptor = Object.getOwnPropertyDescriptor(value, key);
  if (descriptor === undefined) return undefined;
  if ("get" in descriptor || "set" in descriptor) {
    throw new InvalidDescriptionError(
      `Accessor "${key}" is executable and cannot appear in a policy specification`,
    );
  }
  return descriptor.value;
}

function yes(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback;
}

function maskOf(value: unknown, path: string): number {
  try {
    return toMask(value as CapabilityInput);
  } catch (error) {
    throw new InvalidDescriptionError(`Invalid capability set at ${path}: ${String(error)}`);
  }
}

function normalizeRevocation(value: unknown, path: string): RevocationAction {
  if (value === undefined || value === "invalidate" || value === null || value === true) {
    return Object.freeze({ kind: "invalidate" as const });
  }
  if (value === false) {
    return Object.freeze({ kind: "attenuate" as const, removeMask: 0 });
  }
  if (value === "attenuate" || value === "deny") {
    return Object.freeze({ kind: "attenuate" as const, removeMask: ALL_CAPABILITY_MASK });
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new InvalidDescriptionError(`Invalid "onRevoke" at ${path}`);
  }
  const spec = value as RevocationSpec;
  const attenuate = readOwn(spec, "attenuate");
  const deny = readOwn(spec, "deny");
  const retain = readOwn(spec, "retain");
  if (attenuate !== undefined || deny !== undefined) {
    const removeMask =
      (attenuate === undefined ? 0 : maskOf(attenuate, `${path}.attenuate`)) |
      (deny === undefined ? 0 : maskOf(deny, `${path}.deny`));
    return Object.freeze({ kind: "attenuate" as const, removeMask });
  }
  if (retain !== undefined) {
    const retainMask = maskOf(retain, `${path}.retain`);
    return Object.freeze({
      kind: "attenuate" as const,
      removeMask: ALL_CAPABILITY_MASK & ~retainMask,
    });
  }
  return Object.freeze({ kind: "invalidate" as const });
}

function normalizeNodes(
  specs: readonly PolicyNodeSpec[] | Readonly<Record<string, PolicyNodeSpec>>,
): PolicyNodeDef[] {
  const result: PolicyNodeDef[] = [];
  if (Array.isArray(specs)) {
    specs.forEach((spec, index) => {
      if (spec === null || typeof spec !== "object") {
        throw new InvalidDescriptionError(`Policy node ${index} must be an object`);
      }
      const id = readOwn(spec, "id");
      if (typeof id !== "string" || id.length === 0) {
        throw new InvalidDescriptionError(`Policy node ${index} needs a string id`);
      }
      const domain = readOwn(spec, "domain");
      if (typeof domain !== "string" || domain.length === 0) {
        throw new InvalidDescriptionError(`Policy node "${id}" needs a domain`);
      }
      result.push(
        Object.freeze({
          id,
          domain,
          capabilities: maskOf(
            readOwn(spec, "capabilities") ??
              readOwn(spec, "caps") ??
              ALL_CAPABILITY_MASK,
            `policy.nodes.${id}.capabilities`,
          ),
        }),
      );
    });
  } else if (specs !== null && typeof specs === "object") {
    for (const id of Object.keys(specs)) {
      if (id.length === 0) {
        throw new InvalidDescriptionError("Policy node ids must be non-empty strings");
      }
      const spec = readOwn(specs, id);
      if (spec === null || typeof spec !== "object" || Array.isArray(spec)) {
        throw new InvalidDescriptionError(`Policy node "${id}" must be an object`);
      }
      const declaredId = readOwn(spec, "id");
      if (declaredId !== undefined && declaredId !== id) {
        throw new InvalidDescriptionError(`Policy node "${id}" has a conflicting id`);
      }
      const domain = readOwn(spec, "domain");
      if (typeof domain !== "string" || domain.length === 0) {
        throw new InvalidDescriptionError(`Policy node "${id}" needs a domain`);
      }
      result.push(
        Object.freeze({
          id,
          domain,
          capabilities: maskOf(
            readOwn(spec, "capabilities") ??
              readOwn(spec, "caps") ??
              ALL_CAPABILITY_MASK,
            `policy.nodes.${id}.capabilities`,
          ),
        }),
      );
    }
  } else {
    throw new InvalidDescriptionError("Policy must provide nodes");
  }
  return result;
}

function configKey(nodeId: string, capsMask: number): string {
  return JSON.stringify([nodeId, capsMask]);
}

function expandEdges(
  value: PolicySpec["edges"],
): Array<{ keyId: string | undefined; spec: PolicyEdgeSpec }> {
  if (value === undefined) return [];
  if (Array.isArray(value)) {
    return value.map((spec) => ({ keyId: undefined, spec }));
  }
  if (value !== null && typeof value === "object") {
    const result: Array<{ keyId: string | undefined; spec: PolicyEdgeSpec }> = [];
    for (const id of Object.keys(value)) {
      if (id.length === 0) {
        throw new InvalidDescriptionError("Policy edge ids must be non-empty strings");
      }
      const spec = readOwn(value, id);
      if (spec === null || typeof spec !== "object" || Array.isArray(spec)) {
        throw new InvalidDescriptionError(`Policy edge "${id}" must be an object`);
      }
      result.push({ keyId: id, spec: spec as PolicyEdgeSpec });
    }
    return result;
  }
  throw new InvalidDescriptionError("Policy edges must be an array or record");
}

function transitionLabel(edge: PolicyEdgeDef, targetDomain: string): string {
  return JSON.stringify([edge.kind, edge.action, targetDomain]);
}

export class PolicyEngine {
  readonly nodes: ReadonlyMap<string, PolicyNodeDef>;
  readonly edges: ReadonlyMap<string, PolicyEdgeDef>;
  readonly entry: string;
  readonly domainIndex: ReadonlyMap<string, readonly PolicyNodeDef[]>;
  readonly #interner = new ClassInterner();
  #partition: BisimulationResult;
  #configStates: readonly LabelledState[];

  constructor(spec: PolicySpec, defaultEntry?: string) {
    const rawNodes = spec.nodes ?? spec.principals;
    if (rawNodes === undefined) {
      throw new InvalidDescriptionError("Policy must provide nodes or principals");
    }
    const nodeList = normalizeNodes(rawNodes);
    const byId = new Map<string, PolicyNodeDef>();
    const domainIndex = new Map<string, PolicyNodeDef[]>();
    for (const node of nodeList) {
      if (byId.has(node.id)) throw new InvalidDescriptionError(`Duplicate policy node: ${node.id}`);
      byId.set(node.id, node);
      let list = domainIndex.get(node.domain);
      if (list === undefined) {
        list = [];
        domainIndex.set(node.domain, list);
      }
      list.push(node);
    }

    const rawEdges = [...expandEdges(spec.edges), ...expandEdges(spec.grants)];
    const edges = new Map<string, PolicyEdgeDef>();
    rawEdges.forEach(({ keyId, spec: edgeSpec }, index) => {
      if (edgeSpec === null || typeof edgeSpec !== "object") {
        throw new InvalidDescriptionError(`Policy edge ${index} must be an object`);
      }
      const from = readOwn(edgeSpec, "from");
      const to = readOwn(edgeSpec, "to");
      if (typeof from !== "string" || !byId.has(from)) {
        throw new PolicyError(`Policy edge ${index} has an unknown source "${String(from)}"`);
      }
      if (typeof to !== "string" || !byId.has(to)) {
        throw new PolicyError(`Policy edge ${index} has an unknown target "${String(to)}"`);
      }
      const rawId = readOwn(edgeSpec, "id");
      if (keyId !== undefined && rawId !== undefined && rawId !== keyId) {
        throw new InvalidDescriptionError(
          `Policy edge "${keyId}" declares a conflicting id "${String(rawId)}"`,
        );
      }
      const id =
        keyId ??
        (typeof rawId === "string" && rawId.length > 0
          ? rawId
          : `${from}->${to}#${index}`);
      if (edges.has(id)) throw new InvalidDescriptionError(`Duplicate policy edge: ${id}`);
      const rawKind = readOwn(edgeSpec, "kind");
      if (rawKind !== undefined && rawKind !== "transfer" && rawKind !== "grant") {
        throw new InvalidDescriptionError(`Invalid policy edge kind on "${id}"`);
      }
      const explicitGrant = readOwn(edgeSpec, "grant");
      const explicitAuthorization = readOwn(edgeSpec, "authorization");
      const grantFlag =
        rawKind === "transfer"
          ? false
          : rawKind === "grant"
            ? true
            : explicitGrant === false ||
                explicitAuthorization === false ||
                readOwn(edgeSpec, "revocable") === false
              ? false
              : true;
      const kind: PolicyEdgeKind = grantFlag ? "grant" : "transfer";
      const actionValue = readOwn(edgeSpec, "action");
      const action =
        typeof actionValue === "string" && actionValue.length > 0
          ? actionValue
          : kind;
      let capabilities = maskOf(
        readOwn(edgeSpec, "capabilities") ?? readOwn(edgeSpec, "caps") ?? ALL_CAPABILITY_MASK,
        `policy.edges.${id}.capabilities`,
      );
      const attenuate = readOwn(edgeSpec, "attenuate");
      const deny = readOwn(edgeSpec, "deny");
      if (attenuate !== undefined) {
        capabilities &= ~maskOf(attenuate, `policy.edges.${id}.attenuate`);
      }
      if (deny !== undefined) {
        capabilities &= ~maskOf(deny, `policy.edges.${id}.deny`);
      }
      edges.set(id, {
        id,
        from,
        to,
        kind,
        action,
        capabilities,
        grant: grantFlag,
        onRevoke: normalizeRevocation(
          readOwn(edgeSpec, "onRevoke"),
          `policy.edges.${id}.onRevoke`,
        ),
        index,
        revoked: yes(readOwn(edgeSpec, "revoked"), false),
      });
    });

    const entryValue = spec.entry ?? spec.start;
    const entry =
      typeof entryValue === "string" && byId.has(entryValue)
        ? entryValue
        : defaultEntry !== undefined && byId.has(defaultEntry)
          ? defaultEntry
          : nodeList[0]?.id;
    if (entry === undefined) throw new InvalidDescriptionError("Policy has no entry node");

    for (const list of domainIndex.values()) Object.freeze(list);
    this.nodes = new FrozenMap(byId);
    this.edges = new FrozenMap(edges);
    this.entry = entry;
    this.domainIndex = new FrozenMap(domainIndex);
    this.#partition = computeBisimulation([], this.#interner);
    this.#configStates = Object.freeze([]);
    this.rebuild();
    Object.freeze(this);
  }

  node(id: string): PolicyNodeDef {
    const node = this.nodes.get(id);
    if (node === undefined) throw new PolicyError(`Unknown policy node: ${id}`);
    return node;
  }

  edge(id: string): PolicyEdgeDef {
    const edge = this.edges.get(id);
    if (edge === undefined) throw new PolicyError(`Unknown policy edge: ${id}`);
    return edge;
  }

  outgoing(nodeId: string): readonly PolicyEdgeDef[] {
    this.node(nodeId);
    const result: PolicyEdgeDef[] = [];
    for (const edge of this.edges.values()) {
      if (edge.from === nodeId && !edge.revoked) result.push(edge);
    }
    return result;
  }

  incoming(nodeId: string): readonly PolicyEdgeDef[] {
    this.node(nodeId);
    const result: PolicyEdgeDef[] = [];
    for (const edge of this.edges.values()) {
      if (edge.to === nodeId && !edge.revoked) result.push(edge);
    }
    return result;
  }

  nodesInDomain(domain: string): readonly PolicyNodeDef[] {
    return this.domainIndex.get(domain) ?? [];
  }

  normalizeConfig(nodeId: string, capabilities: CapabilityInput): PolicyConfig {
    const node = this.node(nodeId);
    const requested = toMask(capabilities);
    return Object.freeze({
      nodeId,
      capsMask: requested & node.capabilities & ALL_CAPABILITY_MASK,
    });
  }

  classOf(config: PolicyConfig): BisimulationClass {
    const node = this.node(config.nodeId);
    const mask = config.capsMask & node.capabilities;
    const cls = this.#partition.classOf(configKey(config.nodeId, mask));
    if (cls === undefined) {
      throw new PolicyError(
        `No bisimulation class for policy configuration ${config.nodeId}:${mask}`,
      );
    }
    return cls;
  }

  configId(config: PolicyConfig): string {
    const node = this.node(config.nodeId);
    return configKey(config.nodeId, config.capsMask & node.capabilities);
  }

  configOf(nodeId: string, capabilities: CapabilityInput = ALL_CAPABILITY_MASK): PolicyConfig {
    return this.normalizeConfig(nodeId, capabilities);
  }

  isBisimilar(left: PolicyConfig, right: PolicyConfig): boolean {
    return this.classOf(left) === this.classOf(right);
  }

  configDomain(config: PolicyConfig): string {
    return this.node(config.nodeId).domain;
  }

  /**
   * Apply an edge to a configuration. Capabilities are always intersected:
   * there is no code path that unions a capability back in.
   */
  traverse(config: PolicyConfig, edgeId: string): PolicyConfig {
    const edge = this.edge(edgeId);
    if (edge.revoked) throw new PolicyError(`Policy edge "${edgeId}" is revoked`);
    if (edge.from !== config.nodeId) {
      throw new PolicyError(
        `Edge "${edgeId}" starts at "${edge.from}", not at "${config.nodeId}"`,
      );
    }
    const target = this.node(edge.to);
    return Object.freeze({
      nodeId: edge.to,
      capsMask: config.capsMask & edge.capabilities & target.capabilities,
    });
  }

  findEdge(nodeId: string, idOrDomain: string, domain?: string): PolicyEdgeDef {
    const direct = this.edges.get(idOrDomain);
    if (direct !== undefined) {
      if (direct.from !== nodeId) {
        throw new PolicyError(`Edge "${idOrDomain}" does not start at policy node "${nodeId}"`);
      }
      if (direct.revoked) throw new PolicyError(`Policy edge "${idOrDomain}" is revoked`);
      if (domain !== undefined && this.node(direct.to).domain !== domain) {
        throw new PolicyError(
          `Edge "${idOrDomain}" targets domain "${this.node(direct.to).domain}", not "${domain}"`,
        );
      }
      return direct;
    }
    const targetDomain = domain ?? idOrDomain;
    const candidates = this.outgoing(nodeId).filter(
      (edge) => this.node(edge.to).domain === targetDomain,
    );
    if (candidates.length === 1) return candidates[0];
    if (candidates.length === 0) {
      throw new PolicyError(
        `No active policy edge from "${nodeId}" into domain "${targetDomain}"`,
      );
    }
    const config = this.configOf(nodeId);
    const targetClasses = candidates.map((edge) =>
      this.classOf(this.traverse(config, edge.id)),
    );
    if (targetClasses.every((token) => token === targetClasses[0])) {
      return [...candidates].sort((a, b) => a.index - b.index)[0];
    }
    throw new PolicyError(
      `Ambiguous policy edges from "${nodeId}" into domain "${targetDomain}": ` +
        candidates.map((edge) => edge.id).join(", "),
    );
  }

  /**
   * Resolve an edge reference against a configuration. If the edge id names
   * an edge on a bisimilar folded node, the corresponding edge of the current
   * representative is used. This keeps path-specific ids usable without ever
   * comparing path strings or requiring a particular node id.
   */
  resolveEdge(
    config: PolicyConfig,
    idOrDomain: string,
    domain?: string,
  ): PolicyEdgeDef {
    const direct = this.edges.get(idOrDomain);
    if (direct === undefined) {
      if (domain !== undefined) {
        throw new PolicyError(`Unknown policy edge: ${idOrDomain}`);
      }
      return this.resolveDomainEdge(config, idOrDomain);
    }
    if (direct.revoked) throw new PolicyError(`Policy edge "${idOrDomain}" is revoked`);
    if (direct.from === config.nodeId) {
      if (domain !== undefined && this.node(direct.to).domain !== domain) {
        throw new PolicyError(
          `Edge "${idOrDomain}" targets domain "${this.node(direct.to).domain}", not "${domain}"`,
        );
      }
      return direct;
    }

    const directSource = this.normalizeConfig(direct.from, config.capsMask);
    if (this.classOf(directSource) !== this.classOf(config)) {
      throw new PolicyError(
        `Edge "${idOrDomain}" starts at a non-bisimilar policy node "${direct.from}"`,
      );
    }
    const desiredTargetClass = this.classOf(
      this.traverse(directSource, direct.id),
    );
    const candidates = this.outgoing(config.nodeId).filter((candidate) => {
      if (candidate.kind !== direct.kind || candidate.action !== direct.action) return false;
      if (domain !== undefined && this.node(candidate.to).domain !== domain) return false;
      const target = this.traverse(config, candidate.id);
      return this.classOf(target) === desiredTargetClass;
    });
    if (candidates.length === 1) return candidates[0];
    if (candidates.length === 0) {
      throw new PolicyError(
        `No bisimilar representative for edge "${idOrDomain}" at policy node "${config.nodeId}"`,
      );
    }
    return [...candidates].sort((a, b) => a.index - b.index)[0];
  }

  private resolveDomainEdge(config: PolicyConfig, domain: string): PolicyEdgeDef {
    const candidates = this.outgoing(config.nodeId).filter(
      (edge) => this.node(edge.to).domain === domain,
    );
    if (candidates.length === 0) {
      throw new PolicyError(
        `No active policy edge from "${config.nodeId}" into domain "${domain}"`,
      );
    }
    if (candidates.length === 1) return candidates[0];
    const targetClasses = candidates.map((edge) =>
      this.classOf(this.traverse(config, edge.id)),
    );
    if (targetClasses.every((token) => token === targetClasses[0])) {
      return [...candidates].sort((a, b) => a.index - b.index)[0];
    }
    throw new PolicyError(
      `Ambiguous policy edges from "${config.nodeId}" into domain "${domain}": ` +
        candidates.map((edge) => edge.id).join(", "),
    );
  }

  revoke(edgeId: string): PolicyEdgeDef {
    const edge = this.edge(edgeId);
    if (edge.revoked) throw new PolicyError(`Policy edge "${edgeId}" is already revoked`);
    edge.revoked = true;
    this.rebuild();
    return edge;
  }

  restore(edgeId: string): PolicyEdgeDef {
    const edge = this.edge(edgeId);
    edge.revoked = false;
    this.rebuild();
    return edge;
  }

  isRevoked(edgeId: string): boolean {
    return this.edge(edgeId).revoked;
  }

  get configStateCount(): number {
    return this.#configStates.length;
  }

  get classCount(): number {
    return this.#partition.classes.length;
  }

  get iterationCount(): number {
    return this.#partition.iterationCount;
  }

  /**
   * Finite-state view of the policy graph's configuration space. Node ids are
   * debug labels only; equivalence is never decided by comparing these strings.
   */
  model(): FiniteStateModel {
    return new FiniteStateModel(
      this.#configStates.map((state) => ({
        id: state.id,
        observation: state.observation,
        transitions: state.transitions,
      })),
    );
  }

  private rebuild(): void {
    const states: Array<{
      id: string;
      observation: string;
      transitions: Array<{ label: string; to: string }>;
    }> = [];
    for (const node of this.nodes.values()) {
      for (let mask = 0; mask <= ALL_CAPABILITY_MASK; mask++) {
        if ((mask & node.capabilities) !== mask) continue;
        states.push({
          id: configKey(node.id, mask),
          observation: JSON.stringify([node.domain, mask]),
          transitions: [],
        });
      }
    }
    const byId = new Map(states.map((state) => [state.id, state]));
    for (const node of this.nodes.values()) {
      for (let mask = 0; mask <= ALL_CAPABILITY_MASK; mask++) {
        if ((mask & node.capabilities) !== mask) continue;
        const source = byId.get(configKey(node.id, mask));
        if (source === undefined) continue;
        for (const edge of this.edges.values()) {
          if (edge.revoked || edge.from !== node.id) continue;
          const target = this.nodes.get(edge.to);
          if (target === undefined) continue;
          const nextMask = mask & edge.capabilities & target.capabilities;
          source.transitions.push({
            label: transitionLabel(edge, target.domain),
            to: configKey(edge.to, nextMask),
          });
        }
      }
    }
    const frozen: LabelledState[] = states.map((state) =>
      Object.freeze({
        id: state.id,
        observation: state.observation,
        transitions: Object.freeze([...state.transitions]),
      }),
    );
    this.#configStates = Object.freeze(frozen);
    this.#partition = computeBisimulation(this.#configStates, this.#interner);
  }
}
