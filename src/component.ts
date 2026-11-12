import { CallScope, ResourceTable } from "./resource-table.ts";

export type InterfaceName = string;

export interface ComponentDefinition {
  readonly name?: string;
  /** Interfaces the component will import if it is instantiated. */
  readonly imports: readonly InterfaceName[];
}

export interface WorldDefinition {
  readonly name?: string;
  /** Interfaces the host authorizes a component instance to import. */
  readonly imports: readonly InterfaceName[] | ReadonlySet<InterfaceName>;
}

export type WorldInput =
  | WorldDefinition
  | readonly InterfaceName[]
  | ReadonlySet<InterfaceName>;

export class WorldAuthorizationError extends Error {
  readonly code = "IMPORT_NOT_AUTHORIZED";
  readonly unauthorizedImports: readonly InterfaceName[];
  readonly worldName: string | undefined;

  constructor(
    unauthorizedImports: readonly InterfaceName[],
    worldName?: string,
  ) {
    super(
      `component imports are not a subset of the authorized world: ${unauthorizedImports.join(", ")}`,
    );
    this.name = "WorldAuthorizationError";
    this.unauthorizedImports = Object.freeze([...unauthorizedImports]);
    this.worldName = worldName;
  }
}

function normalizeName(name: unknown, field: string): InterfaceName {
  if (typeof name !== "string" || name.trim().length === 0) {
    throw new TypeError(`${field} must contain non-empty interface names`);
  }
  return name;
}

function isImportCollection(
  value: unknown,
): value is readonly InterfaceName[] | ReadonlySet<InterfaceName> {
  return Array.isArray(value) || value instanceof Set;
}

function normalizeInterfaces(
  imports: readonly InterfaceName[] | ReadonlySet<InterfaceName>,
  field: string,
): Set<InterfaceName> {
  if (!isImportCollection(imports)) {
    throw new TypeError(`${field} must be an array or Set`);
  }
  const normalized = new Set<InterfaceName>();
  for (const name of imports) {
    normalized.add(normalizeName(name, field));
  }
  return normalized;
}

export function normalizeWorld(world: WorldInput): {
  name?: string;
  imports: Set<InterfaceName>;
} {
  if (isImportCollection(world)) {
    return { imports: normalizeInterfaces(world, "authorized world imports") };
  }
  if (world === null || typeof world !== "object") {
    throw new TypeError("authorized world must be an import list or world object");
  }
  return {
    name: typeof world.name === "string" ? world.name : undefined,
    imports: normalizeInterfaces(world.imports, "authorized world imports"),
  };
}

export function normalizeComponent(definition: ComponentDefinition): {
  name?: string;
  imports: Set<InterfaceName>;
} {
  if (definition === null || typeof definition !== "object") {
    throw new TypeError("component definition must be an object");
  }
  return {
    name: typeof definition.name === "string" ? definition.name : undefined,
    imports: normalizeInterfaces(
      definition.imports,
      "component definition imports",
    ),
  };
}

/**
 * Implements the world-subset rule for instantiation. Names are compared as
 * exact interface identities; a component cannot gain authority by supplying
 * duplicate, prefixed, or prototype-like names.
 */
export function assertImportsAuthorized(
  componentImports: readonly InterfaceName[] | ReadonlySet<InterfaceName>,
  world: WorldInput,
): {
  imports: readonly InterfaceName[];
  worldImports: readonly InterfaceName[];
  worldName: string | undefined;
} {
  const requested = normalizeInterfaces(componentImports, "component imports");
  const authorizedWorld = normalizeWorld(world);
  const unauthorized = [...requested].filter(
    (name) => !authorizedWorld.imports.has(name),
  );

  if (unauthorized.length > 0) {
    throw new WorldAuthorizationError(unauthorized, authorizedWorld.name);
  }

  return {
    imports: Object.freeze([...requested]),
    worldImports: Object.freeze([...authorizedWorld.imports]),
    worldName: authorizedWorld.name,
  };
}

export interface InstantiateOptions {
  /** A table may be supplied when the host already owns boundary resources. */
  readonly table?: ResourceTable;
}

export class ComponentInstance {
  readonly componentName: string | undefined;
  readonly worldName: string | undefined;
  readonly imports: readonly InterfaceName[];
  readonly table: ResourceTable;

  constructor(
    componentName: string | undefined,
    worldName: string | undefined,
    imports: readonly InterfaceName[],
    table: ResourceTable,
  ) {
    this.componentName = componentName;
    this.worldName = worldName;
    this.imports = imports;
    this.table = table;
  }

  /**
   * Runs one component invocation. Every borrow minted through this scope is
   * revoked in a finally block, even when the component throws.
   */
  invoke<T>(body: (scope: CallScope) => T): T {
    return this.table.withCall(body);
  }

  async invokeAsync<T>(
    body: (scope: CallScope) => T | PromiseLike<T>,
  ): Promise<T> {
    return this.table.withCallAsync(body);
  }
}

export function instantiateComponent(
  definition: ComponentDefinition,
  world: WorldInput,
  options: InstantiateOptions = {},
): ComponentInstance {
  const component = normalizeComponent(definition);
  const authorized = assertImportsAuthorized(component.imports, world);

  return new ComponentInstance(
    component.name,
    authorized.worldName,
    authorized.imports,
    options.table ?? new ResourceTable(),
  );
}

export const instantiate = instantiateComponent;
