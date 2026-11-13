import {
  collectSelectionsForType,
  containsIntrospection,
  findFieldOccurrences,
  parseGraphQLDocument,
  queryDepth,
  resolveValue,
  responseKey,
  selectOperation,
  type FieldNode,
  type GraphQLDocument,
  type OperationNode,
  type SelectionNode,
} from "./graphql.ts";
import {
  bodyToBytes,
  verifyGatewaySignature,
  type BodyInput,
  type GatewaySecret,
  type HeaderBag,
} from "./signature.ts";

export const DEFAULT_MAX_REPRESENTATIONS = 100;
export const DEFAULT_MAX_QUERY_DEPTH = 12;

export interface EntityTypeDefinition {
  readonly keyFields?: readonly string[];
  readonly keys?: readonly string[] | readonly (readonly string[])[];
  readonly key?: string;
  readonly keyField?: string;
  readonly fields?: readonly string[];
  readonly publicFields?: readonly string[];
}

export type EntityDefinition = readonly string[] | EntityTypeDefinition;
export type EntityDefinitions = Readonly<Record<string, EntityDefinition>>;

export interface SubgraphRequest {
  readonly headers: HeaderBag;
  readonly body: BodyInput;
  readonly rawBody?: BodyInput;
}

export interface EntityAuthorizationInput {
  readonly typename: string;
  readonly entity: Readonly<Record<string, unknown>>;
  readonly field: string;
  readonly responseKey: string;
  readonly path: readonly (string | number)[];
  readonly selections: readonly SelectionNode[];
  readonly context?: unknown;
}

export interface EntityAuthorizationDecisionObject {
  readonly allowed: boolean;
  readonly reason?: string;
}

export type EntityAuthorizationDecision =
  | boolean
  | EntityAuthorizationDecisionObject;

export type EntityAuthorizationInputCallback = (
  input: EntityAuthorizationInput,
) => EntityAuthorizationDecision | Promise<EntityAuthorizationDecision>;

export type EntityFieldAuthorizationCallback = (
  typename: string,
  field: string,
) => EntityAuthorizationDecision | Promise<EntityAuthorizationDecision>;

export type EntityTypeAuthorizationCallback = (
  typename: string,
  entity: Readonly<Record<string, unknown>>,
  field: string,
  input: EntityAuthorizationInput,
) => EntityAuthorizationDecision | Promise<EntityAuthorizationDecision>;

export type EntityAuthorizationCallback =
  | EntityAuthorizationInputCallback
  | EntityFieldAuthorizationCallback
  | EntityTypeAuthorizationCallback;

export interface RequestAdmissionOptions {
  readonly secret: GatewaySecret | readonly GatewaySecret[];
  readonly entities?: EntityDefinitions;
  readonly entityKeys?: EntityDefinitions;
  readonly entityWhitelist?: readonly string[] | EntityDefinitions;
  readonly entityKeyFields?: EntityDefinitions;
  readonly authorize?: EntityAuthorizationInputCallback;
  readonly authorizeField?: EntityFieldAuthorizationCallback;
  readonly authorizeEntity?: EntityTypeAuthorizationCallback;
  readonly context?: unknown;
  readonly production?: boolean;
  readonly environment?: string;
  readonly now?: () => number;
  readonly maxClockSkewMs?: number;
  readonly maxRepresentations?: number;
  readonly maxQueryDepth?: number;
  readonly signatureHeaders?: readonly string[];
  readonly timestampHeaders?: readonly string[];
}

export type AdmissionErrorCode =
  | "SERVER_CONFIGURATION"
  | "INVALID_REQUEST"
  | "BATCH_NOT_ALLOWED"
  | "MISSING_GATEWAY_SIGNATURE"
  | "MISSING_GATEWAY_TIMESTAMP"
  | "STALE_GATEWAY_TIMESTAMP"
  | "INVALID_GATEWAY_SIGNATURE"
  | "INVALID_GRAPHQL"
  | "INTROSPECTION_DISABLED"
  | "QUERY_TOO_DEEP"
  | "ENTITY_WHITELIST_REQUIRED"
  | "INVALID_REPRESENTATIONS"
  | "TOO_MANY_REPRESENTATIONS"
  | "UNKNOWN_ENTITY_TYPE"
  | "INVALID_ENTITY_KEY"
  | "EXTRA_ENTITY_FIELD";

export interface AdmissionFailure {
  readonly ok: false;
  readonly allowed: false;
  readonly status: 400 | 401 | 403 | 413 | 500;
  readonly code: AdmissionErrorCode;
  readonly message: string;
  readonly details?: Readonly<Record<string, unknown>>;
}

export interface RequestedEntityField {
  readonly field: string;
  readonly responseKey: string;
  readonly selections: readonly SelectionNode[];
}

export interface EntityRequest {
  readonly typename: string;
  readonly representation: Readonly<Record<string, unknown>>;
  readonly requestedFields: readonly RequestedEntityField[];
  readonly operationFieldIndex: number;
  readonly representationIndex: number;
}

export interface FieldDenial {
  readonly typename: string;
  readonly field: string;
  readonly responseKey: string;
  readonly path: readonly (string | number)[];
  readonly reason: string;
}

export interface FieldAuthorizationError {
  readonly message: string;
  readonly path: readonly (string | number)[];
}

export interface AuthorizedEntityResult {
  readonly typename: string;
  readonly entity: Record<string, unknown> | null;
  readonly data: Record<string, unknown> | null;
  readonly deniedFields: readonly string[];
  readonly denied: readonly FieldDenial[];
  readonly errors: readonly FieldAuthorizationError[];
}

export interface AuthorizeEntityOptions {
  readonly index?: number;
  readonly pathPrefix?: readonly (string | number)[];
  readonly requestedFields?: readonly RequestedEntityField[];
}

export interface RequestAdmissionSuccess {
  readonly ok: true;
  readonly allowed: true;
  readonly operationName: string | null;
  readonly operationType: "query" | "mutation" | "subscription";
  readonly depth: number;
  readonly variables: Readonly<Record<string, unknown>>;
  readonly entityRequests: readonly EntityRequest[];
  readonly authorizeEntity: (
    typename: string,
    entity: Readonly<Record<string, unknown>> | null,
    options?: AuthorizeEntityOptions | number,
  ) => Promise<AuthorizedEntityResult>;
  readonly requestedFieldsFor: (typename: string) => readonly RequestedEntityField[];
}

export type AdmissionResult = RequestAdmissionSuccess | AdmissionFailure;
export type SubgraphAdmissionMiddleware = (
  request: SubgraphRequest,
) => Promise<AdmissionResult>;

interface GraphQLRequestBody {
  readonly query: string;
  readonly variables: Readonly<Record<string, unknown>>;
  readonly operationName?: string | null;
}

interface NormalizedEntity {
  readonly keySets: readonly (readonly string[])[];
  readonly allowedFields: ReadonlySet<string> | null;
}

interface NormalizedEntities {
  readonly entities: ReadonlyMap<string, NormalizedEntity>;
  readonly whitelist: readonly string[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function hasOwn(record: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(record, key);
}

function failure(
  code: AdmissionErrorCode,
  message: string,
  status: AdmissionFailure["status"],
  details?: Readonly<Record<string, unknown>>,
): AdmissionFailure {
  return { ok: false, allowed: false, status, code, message, details };
}

function parseRequestBody(rawBody: BodyInput): unknown {
  if (typeof rawBody !== "string" && !(rawBody instanceof Uint8Array)) {
    throw new TypeError("The request body must be raw JSON text or bytes");
  }

  const text = typeof rawBody === "string"
    ? rawBody
    : new TextDecoder("utf-8", { fatal: true }).decode(rawBody);
  const withoutBom = text.charCodeAt(0) === 0xfeff ? text.slice(1) : text;
  return JSON.parse(withoutBom);
}

function parseGraphQLRequestBody(parsed: unknown): GraphQLRequestBody {
  if (!isRecord(parsed)) {
    throw new TypeError("The GraphQL request body must be a JSON object");
  }
  if (typeof parsed.query !== "string" || parsed.query.trim() === "") {
    throw new TypeError("The GraphQL request body must contain a non-empty query string");
  }

  let operationName: string | null | undefined;
  if (parsed.operationName !== undefined && parsed.operationName !== null) {
    if (typeof parsed.operationName !== "string") {
      throw new TypeError("operationName must be a string");
    }
    operationName = parsed.operationName;
  }

  let variables: Readonly<Record<string, unknown>> = {};
  if (parsed.variables !== undefined && parsed.variables !== null) {
    if (!isRecord(parsed.variables)) {
      throw new TypeError("variables must be a JSON object");
    }
    variables = parsed.variables;
  }

  return {
    query: parsed.query,
    variables,
    operationName,
  };
}

function normalizeKeySets(definition: EntityDefinition): readonly (readonly string[])[] {
  if (Array.isArray(definition)) {
    if (definition.every((value) => typeof value === "string")) {
      return [definition as readonly string[]];
    }

    const keySets = definition as readonly (readonly string[])[];
    if (keySets.every((keys) => Array.isArray(keys) && keys.every((key) => typeof key === "string"))) {
      return keySets;
    }
    throw new TypeError("Entity key declarations must contain field names");
  }

  if (!isRecord(definition)) {
    throw new TypeError("An entity definition must be a key-field array or an object");
  }

  const raw =
    definition.keyFields ??
    definition.keyField ??
    definition.key ??
    definition.keys;

  if (typeof raw === "string") {
    return [[raw]];
  }

  if (Array.isArray(raw)) {
    if (raw.every((value) => typeof value === "string")) {
      return [raw as readonly string[]];
    }
    if (raw.every((value) => Array.isArray(value))) {
      return raw as readonly (readonly string[])[];
    }
  }

  throw new TypeError("Each entity definition must declare one or more key fields");
}

function normalizeAllowedFields(definition: EntityDefinition): ReadonlySet<string> | null {
  if (Array.isArray(definition) || !isRecord(definition)) {
    return null;
  }

  const fields = definition.fields ?? definition.publicFields;
  if (fields === undefined) {
    return null;
  }
  if (!Array.isArray(fields) || !fields.every((field) => typeof field === "string")) {
    throw new TypeError("Entity public field declarations must be string arrays");
  }
  return new Set(fields);
}

function collectEntitySources(options: RequestAdmissionOptions): NodeLikeDefinitionSources {
  const sources: EntityDefinitions[] = [];
  if (isRecord(options.entities)) {
    sources.push(options.entities);
  }
  if (isRecord(options.entityKeys)) {
    sources.push(options.entityKeys);
  }
  if (isRecord(options.entityKeyFields)) {
    sources.push(options.entityKeyFields);
  }
  if (isRecord(options.entityWhitelist)) {
    sources.push(options.entityWhitelist);
  }

  let names: string[] | null = null;
  if (Array.isArray(options.entityWhitelist)) {
    names = options.entityWhitelist.filter((name): name is string => typeof name === "string");
  }

  return { sources, names };
}

interface NodeLikeDefinitionSources {
  readonly sources: readonly EntityDefinitions[];
  readonly names: readonly string[] | null;
}

function normalizeEntities(options: RequestAdmissionOptions): NormalizedEntities {
  const { sources, names: whitelistNames } = collectEntitySources(options);
  if (sources.length === 0 && whitelistNames === null) {
    return { entities: new Map(), whitelist: [] };
  }

  const merged: Record<string, EntityDefinition> = {};
  for (const source of sources) {
    for (const [typename, definition] of Object.entries(source)) {
      merged[typename] = definition;
    }
  }

  const names = whitelistNames === null ? Object.keys(merged) : [...whitelistNames];
  const entities = new Map<string, NormalizedEntity>();

  for (const typename of names) {
    if (!hasOwn(merged, typename)) {
      throw new TypeError(`Entity ${JSON.stringify(typename)} has no key declaration`);
    }
    const definition = merged[typename];
    const keySets = normalizeKeySets(definition)
      .map((keys) => [...new Set(keys)])
      .filter((keys) => keys.length > 0);
    if (keySets.length === 0) {
      throw new TypeError(`Entity ${JSON.stringify(typename)} has no key fields`);
    }
    entities.set(typename, {
      keySets,
      allowedFields: normalizeAllowedFields(definition),
    });
  }

  return { entities, whitelist: names };
}

function isProduction(options: RequestAdmissionOptions): boolean {
  if (typeof options.production === "boolean") {
    return options.production;
  }

  const environment =
    options.environment ??
    (globalThis as { process?: { env?: Record<string, string | undefined> } })
      .process?.env?.NODE_ENV;
  return environment === "production";
}

function representationKeysMatch(
  representation: Readonly<Record<string, unknown>>,
  keySets: readonly (readonly string[])[],
): { readonly ok: true; readonly keySet: readonly string[] } | { readonly ok: false; readonly code: "INVALID_ENTITY_KEY" | "EXTRA_ENTITY_FIELD"; readonly keys: readonly string[] } {
  const actualKeys = Object.keys(representation);
  const declaredKeys = new Set(["__typename"]);
  for (const keySet of keySets) {
    for (const key of keySet) {
      declaredKeys.add(key);
    }
  }
  const extra = actualKeys.filter((key) => !declaredKeys.has(key));
  if (extra.length > 0) {
    return {
      ok: false,
      code: "EXTRA_ENTITY_FIELD",
      keys: extra,
    };
  }

  for (const keySet of keySets) {
    const missing = keySet.filter((key) => !hasOwn(representation, key));
    const extraForKeySet = actualKeys.filter(
      (key) => key !== "__typename" && !keySet.includes(key),
    );
    if (missing.length === 0 && extraForKeySet.length === 0) {
      return { ok: true, keySet };
    }
  }

  return { ok: false, code: "INVALID_ENTITY_KEY", keys: actualKeys };
}

function requestedFieldsForSelectionSet(
  document: GraphQLDocument,
  selections: readonly SelectionNode[],
  typename: string,
): RequestedEntityField[] {
  const fields = collectSelectionsForType(document, selections, typename);
  const responseKeys = new Map<string, string>();
  const result: RequestedEntityField[] = [];

  for (const field of fields) {
    const key = responseKey(field);
    const existing = responseKeys.get(key);
    if (existing !== undefined && existing !== field.name) {
      throw new Error(
        `Conflicting selections for response key ${JSON.stringify(key)}: ` +
        `${JSON.stringify(existing)} and ${JSON.stringify(field.name)}`,
      );
    }
    if (existing === undefined) {
      responseKeys.set(key, field.name);
      result.push({
        field: field.name,
        responseKey: key,
        selections: field.selections,
      });
    }
  }

  return result;
}

function validateEntityRequests(
  document: GraphQLDocument,
  operation: OperationNode,
  variables: Readonly<Record<string, unknown>>,
  normalizedEntities: NormalizedEntities,
  maximumRepresentations: number,
): EntityRequest[] {
  const occurrences = findFieldOccurrences(document, operation).filter(
    ({ field }) => field.name === "_entities",
  );
  if (occurrences.length === 0) {
    return [];
  }

  if (normalizedEntities.entities.size === 0) {
    throw new AdmissionInputError(
      "ENTITY_WHITELIST_REQUIRED",
      "An entity whitelist and key declaration are required for _entities requests",
      500,
    );
  }

  const requests: EntityRequest[] = [];
  let totalRepresentations = 0;

  occurrences.forEach(({ field }, operationFieldIndex) => {
    const argument = hasOwn(field.arguments, "representations")
      ? field.arguments.representations
      : undefined;
    if (argument === undefined) {
      throw new AdmissionInputError(
        "INVALID_REPRESENTATIONS",
        "_entities requires a representations argument",
        400,
        { operationFieldIndex },
      );
    }

    const representations = resolveValue(argument, variables);
    if (!Array.isArray(representations)) {
      throw new AdmissionInputError(
        "INVALID_REPRESENTATIONS",
        "_entities representations must be an array",
        400,
        { operationFieldIndex },
      );
    }

    totalRepresentations += representations.length;
    if (totalRepresentations > maximumRepresentations) {
      throw new AdmissionInputError(
        "TOO_MANY_REPRESENTATIONS",
        `_entities accepts at most ${maximumRepresentations} representations`,
        413,
        { maximumRepresentations, requestedRepresentations: totalRepresentations },
      );
    }

    representations.forEach((representation, representationIndex) => {
      if (!isRecord(representation)) {
        throw new AdmissionInputError(
          "INVALID_REPRESENTATIONS",
          "Every representation must be an object",
          400,
          { operationFieldIndex, representationIndex },
        );
      }

      const typename = representation.__typename;
      if (typeof typename !== "string" || !normalizedEntities.entities.has(typename)) {
        throw new AdmissionInputError(
          "UNKNOWN_ENTITY_TYPE",
          "Representation __typename is missing or not registered for this subgraph",
          400,
          { operationFieldIndex, representationIndex, typename },
        );
      }

      const entity = normalizedEntities.entities.get(typename);
      if (entity === undefined) {
        throw new AdmissionInputError(
          "UNKNOWN_ENTITY_TYPE",
          "Representation __typename is not registered for this subgraph",
          400,
          { operationFieldIndex, representationIndex, typename },
        );
      }

      const keyMatch = representationKeysMatch(representation, entity.keySets);
      if (!keyMatch.ok) {
        throw new AdmissionInputError(
          keyMatch.code,
          keyMatch.code === "EXTRA_ENTITY_FIELD"
            ? "A representation contains fields outside its declared entity key"
            : "A representation does not contain the entity's declared key fields",
          400,
          {
            operationFieldIndex,
            representationIndex,
            typename,
            keys: keyMatch.keys,
            declaredKeys: entity.keySets,
          },
        );
      }

      const requestedFields = requestedFieldsForSelectionSet(
        document,
        field.selections,
        typename,
      );

      requests.push({
        typename,
        representation,
        requestedFields,
        operationFieldIndex,
        representationIndex,
      });
    });
  });

  return requests;
}

class AdmissionInputError extends Error {
  readonly code: AdmissionErrorCode;
  readonly status: AdmissionFailure["status"];
  readonly details?: Readonly<Record<string, unknown>>;

  constructor(
    code: AdmissionErrorCode,
    message: string,
    status: AdmissionFailure["status"],
    details?: Readonly<Record<string, unknown>>,
  ) {
    super(message);
    this.name = "AdmissionInputError";
    this.code = code;
    this.status = status;
    this.details = details;
  }
}

function normalizeAuthorizationCallback(
  options: RequestAdmissionOptions,
): EntityAuthorizationCallback | null {
  return options.authorize ?? options.authorizeField ?? options.authorizeEntity ?? null;
}

function normalizeDecision(decision: EntityAuthorizationDecision): {
  readonly allowed: boolean;
  readonly reason: string;
} {
  if (typeof decision === "boolean") {
    return { allowed: decision, reason: decision ? "" : "authorization denied" };
  }
  if (isRecord(decision) && typeof decision.allowed === "boolean") {
    return {
      allowed: decision.allowed,
      reason: typeof decision.reason === "string" && decision.reason !== ""
        ? decision.reason
        : decision.allowed ? "" : "authorization denied",
    };
  }
  return { allowed: false, reason: "authorization callback returned an invalid decision" };
}

async function invokeAuthorization(
  callback: EntityAuthorizationCallback,
  input: EntityAuthorizationInput,
): Promise<{ readonly allowed: boolean; readonly reason: string }> {
  const callable = callback as (
    ...args: unknown[]
  ) => EntityAuthorizationDecision | Promise<EntityAuthorizationDecision>;

  try {
    const decision = callback.length >= 3
      ? await callable(input.typename, input.entity, input.field, input)
      : callback.length === 2
        ? await callable(input.typename, input.field, input)
        : await callable(input);
    return normalizeDecision(decision);
  } catch {
    return { allowed: false, reason: "authorization callback failed" };
  }
}

function isObjectValue(value: unknown): value is Readonly<Record<string, unknown>> {
  return isRecord(value);
}

function nestedTypename(value: Readonly<Record<string, unknown>>, fallback: string): string {
  return typeof value.__typename === "string" ? value.__typename : fallback;
}

async function filterValueForFields(
  value: unknown,
  selections: readonly SelectionNode[],
  typename: string,
  document: GraphQLDocument,
  options: RequestAdmissionOptions,
  callback: EntityAuthorizationCallback | null,
  allowedFields: ReadonlySet<string> | null,
  path: readonly (string | number)[],
  denied: FieldDenial[],
  errors: FieldAuthorizationError[],
): Promise<unknown> {
  if (Array.isArray(value)) {
    return Promise.all(
      value.map((item, index) =>
        filterValueForFields(
          item,
          selections,
          typename,
          document,
          options,
          callback,
          null,
          [...path, index],
          denied,
          errors,
        ),
      ),
    );
  }

  if (!isObjectValue(value)) {
    return value;
  }

  const fields = requestedFieldsForSelectionSet(
    document,
    selections,
    nestedTypename(value, typename),
  );
  const result: Record<string, unknown> = {};

  for (const requested of fields) {
    const fieldPath = [...path, requested.responseKey];

    if (requested.field === "__typename") {
      result[requested.responseKey] =
        typeof value.__typename === "string" ? value.__typename : nestedTypename(value, typename);
      continue;
    }

    if (!hasOwn(value, requested.field)) {
      continue;
    }

    let allowed = callback !== null;
    let reason = callback === null ? "authorization callback is not configured" : "";
    if (allowedFields !== null && !allowedFields.has(requested.field)) {
      allowed = false;
      reason = "field is outside the entity public field whitelist";
    }

    if (allowed && callback !== null) {
      const decision = await invokeAuthorization(callback, {
        typename: nestedTypename(value, typename),
        entity: value,
        field: requested.field,
        responseKey: requested.responseKey,
        path: fieldPath,
        selections: requested.selections,
        context: options.context,
      });
      allowed = decision.allowed;
      reason = decision.reason;
    }

    if (!allowed) {
      denied.push({
        typename: nestedTypename(value, typename),
        field: requested.field,
        responseKey: requested.responseKey,
        path: fieldPath,
        reason,
      });
      errors.push({
        message: `Field ${JSON.stringify(requested.field)} is not authorized`,
        path: fieldPath,
      });
      continue;
    }

    let fieldValue = value[requested.field];
    if (requested.selections.length > 0 && fieldValue !== null && typeof fieldValue === "object") {
      fieldValue = await filterValueForFields(
        fieldValue,
        requested.selections,
        nestedTypename(value, typename),
        document,
        options,
        callback,
        null,
        [...path, requested.responseKey],
        denied,
        errors,
      );
    }
    result[requested.responseKey] = fieldValue;
  }

  return result;
}

async function authorizeEntityFields(
  document: GraphQLDocument,
  options: RequestAdmissionOptions,
  entityAllowedFields: ReadonlySet<string> | null,
  requestedFields: readonly RequestedEntityField[],
  typename: string,
  entity: Readonly<Record<string, unknown>> | null,
  pathPrefix: readonly (string | number)[],
): Promise<AuthorizedEntityResult> {
  if (entity === null) {
    return {
      typename,
      entity: null,
      data: null,
      deniedFields: [],
      denied: [],
      errors: [],
    };
  }

  const callback = normalizeAuthorizationCallback(options);
  const denied: FieldDenial[] = [];
  const errors: FieldAuthorizationError[] = [];
  const data = await filterValueForFields(
    entity,
    requestedFields.map((field) => selectionForRequestedField(field)),
    typename,
    document,
    options,
    callback,
    entityAllowedFields,
    pathPrefix,
    denied,
    errors,
  );
  const filtered = isRecord(data) ? data : {};
  const entityResult: Record<string, unknown> = {};
  for (const field of requestedFields) {
    if (hasOwn(filtered, field.responseKey)) {
      entityResult[field.field] = filtered[field.responseKey];
    }
  }

  return {
    typename,
    entity: data === null ? null : entityResult,
    data: data === null ? null : filtered,
    deniedFields: denied
      .filter((item) => item.path.length === pathPrefix.length + 1)
      .map((item) => item.field),
    denied,
    errors,
  };
}

function selectionForRequestedField(requested: RequestedEntityField): FieldNode {
  return {
    kind: "field",
    name: requested.field,
    alias: requested.responseKey === requested.field ? null : requested.responseKey,
    arguments: {},
    directives: [],
    selections: requested.selections,
    location: { offset: 0 },
  };
}

export async function admitSubgraphRequest(
  options: RequestAdmissionOptions,
  request: SubgraphRequest,
): Promise<AdmissionResult> {
  if (options.secret === undefined || options.secret === null) {
    return failure("SERVER_CONFIGURATION", "A gateway HMAC secret must be configured", 500);
  }

  const rawBody = request.rawBody ?? request.body;
  if (rawBody === undefined || rawBody === null) {
    return failure("INVALID_REQUEST", "A raw request body is required", 400);
  }
  if (typeof rawBody !== "string" && !(rawBody instanceof Uint8Array)) {
    return failure(
      "INVALID_REQUEST",
      "The middleware requires the exact raw request body for signature verification",
      400,
    );
  }

  const verification = verifyGatewaySignature({
    secret: options.secret,
    body: rawBody,
    headers: request.headers,
    now: options.now,
    maxClockSkewMs: options.maxClockSkewMs,
    signatureHeaders: options.signatureHeaders,
    timestampHeaders: options.timestampHeaders,
  });

  if (!verification.ok) {
    return failure(verification.code, verification.message, 401);
  }

  let parsed: unknown;
  try {
    parsed = parseRequestBody(rawBody);
  } catch {
    return failure("INVALID_REQUEST", "The request body is not valid JSON", 400);
  }

  if (Array.isArray(parsed)) {
    return failure(
      "BATCH_NOT_ALLOWED",
      "GraphQL batch requests are not accepted by this subgraph",
      400,
    );
  }

  let graphqlRequest: GraphQLRequestBody;
  try {
    graphqlRequest = parseGraphQLRequestBody(parsed);
  } catch (error) {
    return failure(
      "INVALID_REQUEST",
      error instanceof Error ? error.message : "Invalid GraphQL request body",
      400,
    );
  }

  let document: GraphQLDocument;
  try {
    document = parseGraphQLDocument(graphqlRequest.query);
  } catch (error) {
    return failure(
      "INVALID_GRAPHQL",
      error instanceof Error ? error.message : "Invalid GraphQL document",
      400,
    );
  }

  let operation: OperationNode;
  try {
    operation = selectOperation(document, graphqlRequest.operationName);
  } catch (error) {
    return failure(
      "INVALID_GRAPHQL",
      error instanceof Error ? error.message : "Invalid GraphQL operation",
      400,
    );
  }

  if (isProduction(options) && containsIntrospection(document, operation)) {
    return failure(
      "INTROSPECTION_DISABLED",
      "Introspection is disabled for this subgraph in production",
      403,
    );
  }

  let depth: number;
  try {
    depth = queryDepth(document, operation);
  } catch (error) {
    return failure(
      "INVALID_GRAPHQL",
      error instanceof Error ? error.message : "Invalid fragment usage",
      400,
    );
  }

  const maximumDepth = Math.min(
    options.maxQueryDepth ?? DEFAULT_MAX_QUERY_DEPTH,
    DEFAULT_MAX_QUERY_DEPTH,
  );
  if (depth > maximumDepth) {
    return failure(
      "QUERY_TOO_DEEP",
      `Query depth ${depth} exceeds the maximum allowed depth of ${maximumDepth}`,
      400,
      { depth, maximumDepth },
    );
  }

  let normalizedEntities: NormalizedEntities;
  try {
    normalizedEntities = normalizeEntities(options);
  } catch (error) {
    return failure(
      "SERVER_CONFIGURATION",
      error instanceof Error ? error.message : "Invalid entity configuration",
      500,
    );
  }

  const maximumRepresentations = Math.min(
    options.maxRepresentations ?? DEFAULT_MAX_REPRESENTATIONS,
    DEFAULT_MAX_REPRESENTATIONS,
  );

  let entityRequests: EntityRequest[];
  try {
    entityRequests = validateEntityRequests(
      document,
      operation,
      graphqlRequest.variables,
      normalizedEntities,
      maximumRepresentations,
    );
  } catch (error) {
    if (error instanceof AdmissionInputError) {
      return failure(error.code, error.message, error.status, error.details);
    }
    return failure(
      "INVALID_REPRESENTATIONS",
      error instanceof Error ? error.message : "Invalid entity representation",
      400,
    );
  }

  const fieldsByType = new Map<string, RequestedEntityField[]>();
  for (const requestInfo of entityRequests) {
    const fields = fieldsByType.get(requestInfo.typename) ?? [];
    const existingKeys = new Set(fields.map((field) => field.responseKey));
    for (const field of requestInfo.requestedFields) {
      if (!existingKeys.has(field.responseKey)) {
        fields.push(field);
        existingKeys.add(field.responseKey);
      }
    }
    fieldsByType.set(requestInfo.typename, fields);
  }

  const authorizeEntity = async (
    typename: string,
    entity: Readonly<Record<string, unknown>> | null,
    authorizeOptions?: AuthorizeEntityOptions | number,
  ): Promise<AuthorizedEntityResult> => {
    const normalizedOptions = typeof authorizeOptions === "number"
      ? { index: authorizeOptions }
      : authorizeOptions ?? {};
    const fields = normalizedOptions.requestedFields ??
      fieldsByType.get(typename) ??
      [];
    const pathPrefix = normalizedOptions.pathPrefix ??
      (normalizedOptions.index === undefined
        ? ["_entities"]
        : ["_entities", normalizedOptions.index]);
    const entityAllowedFields =
      normalizedEntities.entities.get(typename)?.allowedFields ?? null;

    return authorizeEntityFields(
      document,
      options,
      entityAllowedFields,
      fields,
      typename,
      entity,
      pathPrefix,
    );
  };

  return {
    ok: true,
    allowed: true,
    operationName: operation.name,
    operationType: operation.operation,
    depth,
    variables: graphqlRequest.variables,
    entityRequests,
    authorizeEntity,
    requestedFieldsFor: (typename: string) => fieldsByType.get(typename) ?? [],
  };
}

export function createSubgraphAdmissionMiddleware(
  options: RequestAdmissionOptions,
): SubgraphAdmissionMiddleware {
  return (request: SubgraphRequest) => admitSubgraphRequest(options, request);
}

export const createRequestAdmissionMiddleware = createSubgraphAdmissionMiddleware;
export const createSubgraphMiddleware = createSubgraphAdmissionMiddleware;
export const requestAdmissionMiddleware = createSubgraphAdmissionMiddleware;

export function authorizeResolvedEntity(
  options: RequestAdmissionOptions,
  typename: string,
  entity: Readonly<Record<string, unknown>> | null,
  requestedFields: readonly RequestedEntityField[],
  document: GraphQLDocument,
  pathPrefix: readonly (string | number)[] = ["_entities", 0],
): Promise<AuthorizedEntityResult> {
  let allowedFields: ReadonlySet<string> | null = null;
  try {
    allowedFields = normalizeEntities(options).entities.get(typename)?.allowedFields ?? null;
  } catch {
    allowedFields = null;
  }
  return authorizeEntityFields(
    document,
    options,
    allowedFields,
    requestedFields,
    typename,
    entity,
    pathPrefix,
  );
}
