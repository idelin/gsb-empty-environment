export const PROCEDURE_TYPES = [
  "query",
  "mutation",
  "subscription",
] as const;

export type ProcedureType = (typeof PROCEDURE_TYPES)[number];

export const ERROR_CODES = {
  BAD_REQUEST: "BAD_REQUEST",
  NOT_FOUND: "NOT_FOUND",
  UNAUTHORIZED: "UNAUTHORIZED",
  METHOD_NOT_SUPPORTED: "METHOD_NOT_SUPPORTED",
  BATCH_TOO_LARGE: "BATCH_TOO_LARGE",
  INTERNAL_SERVER_ERROR: "INTERNAL_SERVER_ERROR",
} as const;

export type ErrorCode = (typeof ERROR_CODES)[keyof typeof ERROR_CODES];

export const BAD_REQUEST = ERROR_CODES.BAD_REQUEST;
export const NOT_FOUND = ERROR_CODES.NOT_FOUND;
export const UNAUTHORIZED = ERROR_CODES.UNAUTHORIZED;
export const METHOD_NOT_SUPPORTED = ERROR_CODES.METHOD_NOT_SUPPORTED;
export const BATCH_TOO_LARGE = ERROR_CODES.BATCH_TOO_LARGE;
export const INTERNAL_SERVER_ERROR = ERROR_CODES.INTERNAL_SERVER_ERROR;

export const MAX_BATCH_SIZE = 10;

export interface ProcedureError {
  code: ErrorCode;
  message: string;
}

export interface ProcedureSuccess<T = unknown> {
  ok: true;
  path: string;
  type: ProcedureType;
  data: T;
}

export interface ProcedureFailure {
  ok: false;
  path: string;
  type?: ProcedureType;
  error: ProcedureError;
}

export type ProcedureResult<T = unknown> =
  | ProcedureSuccess<T>
  | ProcedureFailure;

export interface BatchResult {
  ok: boolean;
  results: ProcedureResult[];
  error?: ProcedureError;
}

export interface AuthorizationContext {
  /** The request context, matching the tRPC-style convention. */
  ctx: unknown;
  context: unknown;
  headers: unknown;
  method: string;
  path: string;
  type: ProcedureType;
  request: TrpcRequest;
  [key: string]: unknown;
}

export interface ProcedureInvocationContext extends AuthorizationContext {
  input: unknown;
}

export type Authorizer = (
  context: AuthorizationContext,
) => unknown | Promise<unknown>;

export interface InputParserObject {
  parse(input: unknown): unknown;
  safeParse?(input: unknown):
    | { success: true; data: unknown }
    | { success: false; error?: unknown };
}

export type InputParser =
  | ((input: unknown) => unknown | Promise<unknown>)
  | InputParserObject;

export type ProcedureResolver = (
  context: ProcedureInvocationContext,
  requestContext?: unknown,
  request?: TrpcRequest,
) => unknown | Promise<unknown>;

export interface ProcedureDefinition {
  type?: ProcedureType;
  kind?: ProcedureType;
  query?: ProcedureResolver;
  mutation?: ProcedureResolver;
  subscription?: ProcedureResolver;
  resolve?: ProcedureResolver;
  resolver?: ProcedureResolver;
  handler?: ProcedureResolver;
  execute?: ProcedureResolver;
  fn?: ProcedureResolver;
  run?: ProcedureResolver;
  authorize?: Authorizer;
  authorization?: Authorizer;
  auth?: Authorizer | boolean;
  guard?: Authorizer;
  isAuthorized?: Authorizer;
  protected?: boolean;
  requiresAuth?: boolean;
  public?: boolean;
  input?: InputParser;
  inputParser?: InputParser;
  parseInput?: InputParser;
  validate?: InputParser;
  validator?: InputParser;
  inputSchema?: InputParser;
  [key: string]: unknown;
}

export interface ProcedureCall {
  path: string;
  input?: unknown;
  inputJson?: string;
  method?: string;
  context?: unknown;
  ctx?: unknown;
  headers?: unknown;
  /** Alias accepted by transports that call the field `procedure`. */
  procedure?: string;
}

export interface TrpcRequest {
  method?: string;
  path?: string;
  url?: string;
  body?: unknown;
  input?: unknown;
  inputJson?: string;
  calls?: ProcedureCall[];
  context?: unknown;
  ctx?: unknown;
  headers?: unknown;
  [key: string]: unknown;
}

export interface RouterOptions {
  authorize?: Authorizer;
  auth?: Authorizer;
  isAuthorized?: Authorizer;
  maxBatchSize?: number;
}

export interface DispatchOptions {
  method?: string;
  context?: unknown;
  ctx?: unknown;
  headers?: unknown;
  request?: TrpcRequest;
}

interface NormalizedProcedure {
  path: string;
  type: ProcedureType;
  resolver: ProcedureResolver;
  authorizer?: Authorizer;
  protected: boolean;
  inputParser?: InputParser;
  definition: ProcedureDefinition | ProcedureResolver;
}

interface RawExecution {
  path: string;
  input: unknown;
  inputJson?: string;
  method?: string;
  context?: unknown;
  headers?: unknown;
  request: TrpcRequest;
}

const PROCEDURE_MARKER = Symbol.for("netnew.procedure");
const BLOCKED_SEGMENTS = new Set(["__proto__", "constructor", "prototype"]);

const HTTP_STATUS_BY_CODE: Record<ErrorCode, number> = {
  BAD_REQUEST: 400,
  NOT_FOUND: 404,
  UNAUTHORIZED: 401,
  METHOD_NOT_SUPPORTED: 405,
  BATCH_TOO_LARGE: 413,
  INTERNAL_SERVER_ERROR: 500,
};

function batchLimit(options: RouterOptions): number {
  const configured = options.maxBatchSize;
  if (configured === undefined) return MAX_BATCH_SIZE;
  if (!Number.isInteger(configured) || configured <= 0) {
    return MAX_BATCH_SIZE;
  }
  return Math.min(MAX_BATCH_SIZE, configured);
}

export class RouterError extends Error {
  readonly code: ErrorCode;

  constructor(code: ErrorCode, message = code) {
    super(message);
    this.name = "RouterError";
    this.code = code;
  }
}

function hasOwn(value: object, key: PropertyKey): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isFunction(value: unknown): value is ProcedureResolver {
  return typeof value === "function";
}

function isProcedureType(value: unknown): value is ProcedureType {
  return (
    typeof value === "string" &&
    (PROCEDURE_TYPES as readonly string[]).includes(value)
  );
}

function isInputParser(value: unknown): value is InputParser {
  if (typeof value === "function") return true;
  return isRecord(value) && typeof value.parse === "function";
}

function isAuthorizer(value: unknown): value is Authorizer {
  return typeof value === "function";
}

function isProcedureDefinition(value: unknown): value is ProcedureDefinition {
  if (!isRecord(value)) return false;
  if (hasOwn(value, PROCEDURE_MARKER)) return true;
  if (isProcedureType(value.type) || isProcedureType(value.kind)) return true;
  return (
    isFunction(value.resolve) ||
    isFunction(value.resolver) ||
    isFunction(value.handler) ||
    isFunction(value.execute) ||
    isFunction(value.fn) ||
    isFunction(value.run) ||
    isFunction(value.query) ||
    isFunction(value.mutation) ||
    isFunction(value.subscription)
  );
}

function makeFailure(
  path: string,
  code: ErrorCode,
  message: string,
  type?: ProcedureType,
): ProcedureFailure {
  return {
    ok: false,
    path,
    ...(type === undefined ? {} : { type }),
    error: { code, message },
  };
}

function sanitizedErrorMessage(
  error: unknown,
  fallback: string,
  fallbackCode: ErrorCode,
): ProcedureError {
  if (
    error instanceof RouterError &&
    error.code !== ERROR_CODES.INTERNAL_SERVER_ERROR
  ) {
    return {
      code: error.code,
      message: error.message || fallback,
    };
  }

  return {
    code: fallbackCode,
    message: fallback,
  };
}

function methodAllows(type: ProcedureType, method: string): boolean {
  return !(method === "GET" && type !== "query");
}

function methodError(type: ProcedureType): string {
  return `${type} procedures cannot be called with GET`;
}

function authorizationAllowed(value: unknown): boolean {
  if (
    value === null ||
    value === undefined ||
    value === false ||
    value === "" ||
    value === 0 ||
    (typeof value === "number" && Number.isNaN(value))
  ) {
    return false;
  }
  if (
    isRecord(value) &&
    hasOwn(value, "authorized") &&
    value.authorized === false
  ) {
    return false;
  }
  return true;
}

function contextAsObject(context: unknown): Record<string, unknown> {
  if (!isRecord(context)) return {};
  const copy: Record<string, unknown> = {};
  for (const key of Object.keys(context)) {
    const descriptor = Object.getOwnPropertyDescriptor(context, key);
    if (descriptor === undefined || !("value" in descriptor)) continue;
    Object.defineProperty(copy, key, {
      configurable: true,
      enumerable: true,
      value: descriptor.value,
      writable: true,
    });
  }
  return copy;
}

function makeAuthorizationContext(
  procedure: NormalizedProcedure,
  invocation: RawExecution,
  method: string,
): AuthorizationContext {
  const safeRequest: TrpcRequest = {
    method,
    path: procedure.path,
    headers: invocation.headers,
  };

  return {
    ...contextAsObject(invocation.context),
    ctx: invocation.context,
    context: invocation.context,
    headers: invocation.headers,
    method,
    path: procedure.path,
    type: procedure.type,
    request: safeRequest,
  };
}

async function runInputParser(
  parser: InputParser | undefined,
  input: unknown,
): Promise<unknown> {
  if (parser === undefined) return input;

  if (typeof parser === "function") {
    return parser(input);
  }

  if (typeof parser.safeParse === "function") {
    const result = await parser.safeParse(input);
    if (result && result.success === true) return result.data;
    throw new RouterError(ERROR_CODES.BAD_REQUEST, "Invalid input");
  }

  return parser.parse(input);
}

function tagProcedure<T extends ProcedureDefinition>(
  procedureDefinition: T,
): ProcedureDefinition {
  Object.defineProperty(procedureDefinition, PROCEDURE_MARKER, {
    configurable: false,
    enumerable: false,
    value: true,
    writable: false,
  });
  return procedureDefinition;
}

function createProcedureFromParts(
  type: ProcedureType,
  resolverOrOptions: ProcedureDefinition | ProcedureResolver,
  options: ProcedureDefinition = {},
): ProcedureDefinition {
  if (typeof resolverOrOptions === "function") {
    return tagProcedure({
      ...options,
      type,
      resolve: resolverOrOptions,
    });
  }

  return tagProcedure({
    ...resolverOrOptions,
    ...options,
    type,
    resolve:
      options.resolve ??
      options.resolver ??
      options.handler ??
      resolverOrOptions.resolve ??
      resolverOrOptions.resolver ??
      resolverOrOptions.handler ??
      resolverOrOptions[type],
  });
}

export interface ProcedureBuilder {
  input(parser: InputParser): ProcedureBuilder;
  authorize(authorizer: Authorizer): ProcedureBuilder;
  use(authorizer: Authorizer): ProcedureBuilder;
  protected(): ProcedureBuilder;
  query(
    resolver: ProcedureResolver | ProcedureDefinition,
    options?: ProcedureDefinition,
  ): ProcedureDefinition;
  mutation(
    resolver: ProcedureResolver | ProcedureDefinition,
    options?: ProcedureDefinition,
  ): ProcedureDefinition;
  subscription(
    resolver: ProcedureResolver | ProcedureDefinition,
    options?: ProcedureDefinition,
  ): ProcedureDefinition;
}

function makeBuilder(base: ProcedureDefinition): ProcedureBuilder {
  return {
    input(parser) {
      return makeBuilder({ ...base, input: parser });
    },
    authorize(authorizer) {
      return makeBuilder({ ...base, authorize: authorizer });
    },
    use(authorizer) {
      return makeBuilder({ ...base, authorize: authorizer });
    },
    protected() {
      return makeBuilder({ ...base, protected: true });
    },
    query(resolver, options = {}) {
      return createProcedureFromParts("query", resolver, {
        ...base,
        ...options,
      });
    },
    mutation(resolver, options = {}) {
      return createProcedureFromParts("mutation", resolver, {
        ...base,
        ...options,
      });
    },
    subscription(resolver, options = {}) {
      return createProcedureFromParts("subscription", resolver, {
        ...base,
        ...options,
      });
    },
  };
}

export function procedure(
  options: ProcedureDefinition = {},
): ProcedureBuilder {
  return makeBuilder(options);
}

export function query(
  resolver: ProcedureResolver | ProcedureDefinition,
  options: ProcedureDefinition = {},
): ProcedureDefinition {
  return createProcedureFromParts("query", resolver, options);
}

export function mutation(
  resolver: ProcedureResolver | ProcedureDefinition,
  options: ProcedureDefinition = {},
): ProcedureDefinition {
  return createProcedureFromParts("mutation", resolver, options);
}

export function subscription(
  resolver: ProcedureResolver | ProcedureDefinition,
  options: ProcedureDefinition = {},
): ProcedureDefinition {
  return createProcedureFromParts("subscription", resolver, options);
}

export function protectedProcedure(): ProcedureBuilder;
export function protectedProcedure(
  resolver: ProcedureResolver | ProcedureDefinition,
  options?: ProcedureDefinition,
): ProcedureDefinition;
export function protectedProcedure(
  resolver?: ProcedureResolver | ProcedureDefinition,
  options: ProcedureDefinition = {},
): ProcedureDefinition | ProcedureBuilder {
  if (resolver === undefined) {
    return makeBuilder({ protected: true });
  }
  const type = isFunction(resolver)
    ? "query"
    : isProcedureType(resolver.type)
      ? resolver.type
      : isProcedureType(resolver.kind)
        ? resolver.kind
        : isFunction(resolver.mutation)
          ? "mutation"
          : isFunction(resolver.subscription)
            ? "subscription"
            : "query";
  return createProcedureFromParts(type, resolver, {
    ...options,
    protected: true,
  });
}

export function publicProcedure(): ProcedureBuilder;
export function publicProcedure(
  resolver: ProcedureResolver | ProcedureDefinition,
  options?: ProcedureDefinition,
): ProcedureDefinition;
export function publicProcedure(
  resolver?: ProcedureResolver | ProcedureDefinition,
  options: ProcedureDefinition = {},
): ProcedureDefinition | ProcedureBuilder {
  if (resolver === undefined) return makeBuilder({ public: true });
  const type = isFunction(resolver)
    ? "query"
    : isProcedureType(resolver.type)
      ? resolver.type
      : isProcedureType(resolver.kind)
        ? resolver.kind
        : isFunction(resolver.mutation)
          ? "mutation"
          : isFunction(resolver.subscription)
            ? "subscription"
            : "query";
  return createProcedureFromParts(type, resolver, {
    ...options,
    public: true,
  });
}

function processName(path: string): string {
  let decoded: string;
  try {
    const pathname = /^[a-z][a-z\d+.-]*:\/\//i.test(path)
      ? new URL(path).pathname
      : path;
    decoded = decodeURIComponent(pathname);
  } catch {
    return "";
  }

  let result = decoded;
  if (result.startsWith("/api/trpc/")) {
    result = result.slice("/api/trpc/".length);
  } else if (result.startsWith("/")) {
    result = result.slice(1);
  }
  return result;
}

function hasBlockedPathSegment(path: string): boolean {
  return path.split(".").some((segment) => BLOCKED_SEGMENTS.has(segment));
}

function normalizeProcedure(
  value: ProcedureDefinition | ProcedureResolver,
  path: string,
): NormalizedProcedure {
  const procedureDefinition: ProcedureDefinition = isFunction(value)
    ? { type: "query", resolve: value }
    : value;

  const typeValue =
    procedureDefinition.type ??
    procedureDefinition.kind ??
    (isFunction(procedureDefinition.mutation)
      ? "mutation"
      : isFunction(procedureDefinition.subscription)
        ? "subscription"
        : "query");

  if (!isProcedureType(typeValue)) {
    throw new TypeError(`Invalid procedure type at ${path}`);
  }

  const resolver =
    procedureDefinition.resolve ??
    procedureDefinition.resolver ??
    procedureDefinition.handler ??
    procedureDefinition.execute ??
    procedureDefinition.fn ??
    procedureDefinition.run ??
    procedureDefinition[typeValue];

  if (!isFunction(resolver)) {
    throw new TypeError(`Procedure ${path} has no resolver`);
  }

  const candidateAuthorizer =
    procedureDefinition.authorize ??
    procedureDefinition.authorization ??
    procedureDefinition.guard ??
    procedureDefinition.isAuthorized ??
    (isFunction(procedureDefinition.auth)
      ? procedureDefinition.auth
      : undefined);

  const authorizer = isAuthorizer(candidateAuthorizer)
    ? candidateAuthorizer
    : undefined;

  const isProtected =
    procedureDefinition.public === true
      ? false
      : procedureDefinition.protected === true ||
        procedureDefinition.requiresAuth === true ||
        procedureDefinition.auth === true ||
        authorizer !== undefined;

  const candidateParser =
    procedureDefinition.inputParser ??
    procedureDefinition.parseInput ??
    procedureDefinition.validate ??
    procedureDefinition.validator ??
    procedureDefinition.inputSchema ??
    procedureDefinition.input;

  const inputParser = isInputParser(candidateParser)
    ? candidateParser
    : undefined;

  return {
    path,
    type: typeValue,
    resolver,
    authorizer,
    protected: isProtected,
    inputParser,
    definition: value,
  };
}

function collectProcedures(
  definitions: Record<string, unknown>,
  procedures: Map<string, NormalizedProcedure>,
  options: RouterOptions,
): void {
  const seenObjects = new WeakSet<object>();

  const addProcedure = (
    path: string,
    value: ProcedureDefinition | ProcedureResolver,
  ): void => {
    if (procedures.has(path)) {
      throw new TypeError(`Duplicate procedure path: ${path}`);
    }
    procedures.set(path, normalizeProcedure(value, path));
  };

  const visit = (node: Record<string, unknown>, prefix: string): void => {
    if (seenObjects.has(node)) return;
    seenObjects.add(node);

    for (const key of Object.keys(node)) {
      const descriptor = Object.getOwnPropertyDescriptor(node, key);
      if (descriptor === undefined || !("value" in descriptor)) continue;

      const value = descriptor.value;
      const path = prefix === "" ? key : `${prefix}.${key}`;
      if (hasBlockedPathSegment(path)) continue;

      if (isFunction(value)) {
        addProcedure(path, value);
        continue;
      }

      if (!isRecord(value)) continue;
      if (isProcedureDefinition(value)) {
        addProcedure(path, value);
      } else {
        visit(value, path);
      }
    }
  };

  visit(definitions, "");
  void options;
}

export class ProcedureRouter {
  readonly procedures: ReadonlyMap<string, NormalizedProcedure>;
  readonly options: Readonly<RouterOptions>;

  constructor(
    definitions: Record<string, unknown> = {},
    options: RouterOptions = {},
  ) {
    const procedures = new Map<string, NormalizedProcedure>();
    collectProcedures(definitions, procedures, options);
    this.procedures = procedures;
    this.options = Object.freeze({ ...options });
  }

  lookup(path: string): NormalizedProcedure | undefined {
    if (typeof path !== "string") return undefined;
    const normalized = processName(path);
    if (normalized === "" || hasBlockedPathSegment(normalized)) {
      return undefined;
    }
    return this.procedures.get(normalized);
  }

  getProcedure(path: string): NormalizedProcedure | undefined {
    return this.lookup(path);
  }

  async dispatch(
    callOrPath: ProcedureCall | string,
    input?: unknown,
    options: DispatchOptions = {},
  ): Promise<ProcedureResult> {
    const call: ProcedureCall =
      typeof callOrPath === "string"
        ? {
            path: callOrPath,
            ...(input === undefined ? {} : { input }),
          }
        : callOrPath;

    const request: TrpcRequest = options.request ?? {
      method: options.method,
      context: options.context,
      ctx: options.ctx,
      headers: options.headers,
    };

    return this.execute({
      path: call.path ?? call.procedure ?? "",
      input:
        hasOwn(call, "input") && call.input !== undefined
          ? call.input
          : request.input,
      inputJson:
        call.inputJson ??
        (hasOwn(call, "input") ? undefined : request.inputJson),
      method: call.method ?? options.method ?? request.method,
      context:
        options.context ??
        call.context ??
        request.context ??
        options.ctx ??
        call.ctx ??
        request.ctx,
      headers: options.headers ?? call.headers ?? request.headers,
      request,
    });
  }

  async dispatchBatch(
    calls: ProcedureCall[],
    options: DispatchOptions = {},
  ): Promise<BatchResult> {
    const limit = batchLimit(this.options);

    if (!Array.isArray(calls)) {
      return {
        ok: false,
        results: [],
        error: {
          code: ERROR_CODES.BAD_REQUEST,
          message: "Batch calls must be an array",
        },
      };
    }

    if (calls.length === 0) {
      return {
        ok: false,
        results: [],
        error: {
          code: ERROR_CODES.BAD_REQUEST,
          message: "Batch must contain at least one call",
        },
      };
    }

    if (calls.length > limit) {
      return {
        ok: false,
        results: [],
        error: {
          code: ERROR_CODES.BATCH_TOO_LARGE,
          message: `Batch size exceeds the limit of ${limit}`,
        },
      };
    }

    const request: TrpcRequest = options.request ?? {
      method: options.method,
      context: options.context,
      ctx: options.ctx,
      headers: options.headers,
      calls,
    };

    const results = await Promise.all(
      calls.map(async (call, index) => {
        const path = call.path ?? call.procedure ?? "";
        try {
          return await this.execute({
            path,
            input:
              hasOwn(call, "input") && call.input !== undefined
                ? call.input
                : undefined,
            inputJson: call.inputJson,
            method: call.method ?? options.method ?? request.method,
            context:
              call.context ??
              options.context ??
              request.context ??
              call.ctx ??
              options.ctx ??
              request.ctx,
            headers:
              call.headers ?? options.headers ?? request.headers,
            request: {
              ...request,
              index,
            },
          });
        } catch {
          return makeFailure(
            processName(path),
            ERROR_CODES.INTERNAL_SERVER_ERROR,
            "Procedure failed",
          );
        }
      }),
    );

    return {
      ok: results.every((result) => result.ok),
      results,
    };
  }

  async handle(
    request: TrpcRequest,
  ): Promise<ProcedureResult | BatchResult> {
    return handleRequest(this, request);
  }

  async handleRequest(
    request: TrpcRequest,
  ): Promise<ProcedureResult | BatchResult> {
    return this.handle(request);
  }

  async handleTrpcRequest(
    request: TrpcRequest,
  ): Promise<ProcedureResult | BatchResult> {
    return this.handle(request);
  }

  async batch(
    calls: ProcedureCall[],
    options: DispatchOptions = {},
  ): Promise<BatchResult> {
    return this.dispatchBatch(calls, options);
  }

  createHandler(): (
    request: TrpcRequest,
  ) => Promise<ProcedureResult | BatchResult> {
    return (request) => this.handle(request);
  }

  private async execute(invocation: RawExecution): Promise<ProcedureResult> {
    const requestedPath =
      typeof invocation.path === "string"
        ? processName(invocation.path.split("?")[0] ?? "")
        : "";
    const procedure = this.lookup(requestedPath);

    if (procedure === undefined) {
      return makeFailure(
        requestedPath,
        ERROR_CODES.NOT_FOUND,
        "Procedure not found",
      );
    }

    const method = (invocation.method ?? "POST").toUpperCase();
    if (!methodAllows(procedure.type, method)) {
      return makeFailure(
        procedure.path,
        ERROR_CODES.METHOD_NOT_SUPPORTED,
        methodError(procedure.type),
        procedure.type,
      );
    }

    if (procedure.protected) {
      const authorizer =
        procedure.authorizer ??
        this.options.authorize ??
        this.options.auth ??
        this.options.isAuthorized;
      if (authorizer === undefined) {
        return makeFailure(
          procedure.path,
          ERROR_CODES.UNAUTHORIZED,
          "Unauthorized",
          procedure.type,
        );
      }

      let authorized: unknown;
      try {
        authorized = await authorizer(
          makeAuthorizationContext(procedure, invocation, method),
        );
      } catch {
        authorized = false;
      }

      if (!authorizationAllowed(authorized)) {
        return makeFailure(
          procedure.path,
          ERROR_CODES.UNAUTHORIZED,
          "Unauthorized",
          procedure.type,
        );
      }
    }

    let rawInput = invocation.input;
    if (invocation.inputJson !== undefined) {
      if (typeof invocation.inputJson !== "string") {
        return makeFailure(
          procedure.path,
          ERROR_CODES.BAD_REQUEST,
          "Invalid JSON input",
          procedure.type,
        );
      }
      try {
        rawInput = parseJsonStrict(invocation.inputJson);
      } catch {
        return makeFailure(
          procedure.path,
          ERROR_CODES.BAD_REQUEST,
          "Invalid JSON input",
          procedure.type,
        );
      }
    }

    let parsedInput: unknown;
    try {
      parsedInput = await runInputParser(procedure.inputParser, rawInput);
    } catch {
      return makeFailure(
        procedure.path,
        ERROR_CODES.BAD_REQUEST,
        "Invalid input",
        procedure.type,
      );
    }

    const invocationContext: ProcedureInvocationContext = {
      ...makeAuthorizationContext(procedure, invocation, method),
      input: parsedInput,
    };

    try {
      const data = await procedure.resolver(
        invocationContext,
        invocation.context,
        invocation.request,
      );
      return {
        ok: true,
        path: procedure.path,
        type: procedure.type,
        data,
      };
    } catch (error) {
      const message =
        error instanceof RouterError
          ? sanitizedErrorMessage(
              error,
              "Procedure failed",
              ERROR_CODES.INTERNAL_SERVER_ERROR,
            )
          : {
              code: ERROR_CODES.INTERNAL_SERVER_ERROR,
              message: "Procedure failed",
            };
      return {
        ok: false,
        path: procedure.path,
        type: procedure.type,
        error: message,
      };
    }
  }
}

export const Router = ProcedureRouter;

export function createRouter(
  definitions: Record<string, unknown> = {},
  options: RouterOptions = {},
): ProcedureRouter {
  return new ProcedureRouter(definitions, options);
}

export const router = createRouter;
export const createProcedureRouter = createRouter;

function failureWithPath(
  code: ErrorCode,
  message: string,
  path = "",
): ProcedureFailure {
  return makeFailure(path, code, message);
}

function unwrapTrpcBody(body: unknown): unknown {
  if (isRecord(body) && hasOwn(body, "json")) {
    return body.json;
  }
  return body;
}

function queryValue(
  url: string | undefined,
  key: string,
): string | undefined {
  if (url === undefined) return undefined;
  const queryIndex = url.indexOf("?");
  if (queryIndex === -1) return undefined;
  const search = new URLSearchParams(url.slice(queryIndex + 1));
  const value = search.get(key);
  return value === null ? undefined : value;
}

function pathFromRequest(request: TrpcRequest): string {
  if (typeof request.path === "string") return request.path;
  if (typeof request.url === "string") {
    return request.url.split("?")[0] ?? "";
  }
  return "";
}

function normalizeBatchPathList(path: string): string[] | undefined {
  const normalized = processName(path);
  if (!normalized.includes(",")) return undefined;
  return normalized.split(",").filter((item) => item.length > 0);
}

function callsFromBody(body: unknown, paths: string[]): ProcedureCall[] {
  if (Array.isArray(body)) {
    return body.map((item, index) => {
      if (isRecord(item) && typeof item.path === "string") {
        return {
          path: item.path,
          input: hasOwn(item, "input")
            ? item.input
            : unwrapTrpcBody(item),
          inputJson:
            typeof item.inputJson === "string" ? item.inputJson : undefined,
        };
      }
      return {
        path: paths[index] ?? "",
        input: unwrapTrpcBody(item),
      };
    });
  }

  if (isRecord(body) && Array.isArray(body.calls)) {
    return callsFromBody(body.calls, paths);
  }

  return paths.map((path, index) => {
    const value = isRecord(body) ? body[String(index)] : undefined;
    return {
      path,
      input: value === undefined ? undefined : unwrapTrpcBody(value),
    };
  });
}

export async function dispatch(
  routerInstance: ProcedureRouter,
  callOrPath: ProcedureCall | string,
  input?: unknown,
  options: DispatchOptions = {},
): Promise<ProcedureResult> {
  return routerInstance.dispatch(callOrPath, input, options);
}

export const dispatchProcedure = dispatch;

export async function dispatchBatch(
  routerInstance: ProcedureRouter,
  calls: ProcedureCall[],
  options: DispatchOptions = {},
): Promise<BatchResult> {
  return routerInstance.dispatchBatch(calls, options);
}

export async function handleRequest(
  routerInstance: ProcedureRouter,
  request: TrpcRequest,
): Promise<ProcedureResult | BatchResult> {
  const requestPath = pathFromRequest(request);
  const batchPaths = normalizeBatchPathList(requestPath);
  const explicitCalls = Array.isArray(request.calls)
    ? request.calls
    : undefined;
  const batchExpected =
    explicitCalls !== undefined ||
    batchPaths !== undefined ||
    (isRecord(request.body) && Array.isArray(request.body.calls));
  const limit = batchLimit(routerInstance.options);

  if (
    (batchPaths !== undefined && batchPaths.length > limit) ||
    (explicitCalls !== undefined && explicitCalls.length > limit)
  ) {
    return {
      ok: false,
      results: [],
      error: {
        code: ERROR_CODES.BATCH_TOO_LARGE,
        message: `Batch size exceeds the limit of ${limit}`,
      },
    };
  }

  const method = (request.method ?? "POST").toUpperCase();
  const context = request.context ?? request.ctx;
  const headers = request.headers;
  let body = request.body;

  if (typeof body === "string" && batchExpected) {
    try {
      body = parseJsonStrict(body);
    } catch {
      const failure = failureWithPath(
        ERROR_CODES.BAD_REQUEST,
        "Invalid JSON",
        processName(requestPath),
      );
      if (batchExpected) {
        return {
          ok: false,
          results: [],
          error: failure.error,
        };
      }
      return failure;
    }
  }

  if (explicitCalls !== undefined) {
    return routerInstance.dispatchBatch(explicitCalls, {
      method,
      context,
      ctx: request.ctx,
      headers,
      request,
    });
  }

  if (batchPaths !== undefined) {
    const calls = callsFromBody(body, batchPaths);
    return routerInstance.dispatchBatch(calls, {
      method,
      context,
      ctx: request.ctx,
      headers,
      request,
    });
  }

  if (isRecord(body) && Array.isArray(body.calls)) {
    return routerInstance.dispatchBatch(body.calls, {
      method,
      context,
      ctx: request.ctx,
      headers,
      request,
    });
  }

  if (Array.isArray(body)) {
    return routerInstance.dispatchBatch(body, {
      method,
      context,
      ctx: request.ctx,
      headers,
      request,
    });
  }

  const inputFromQuery = queryValue(request.url, "input");
  const inputJsonFromQuery =
    request.inputJson ?? queryValue(request.url, "inputJson") ?? inputFromQuery;
  const inputFromBody =
    request.body === undefined || typeof request.body === "string"
      ? undefined
      : unwrapTrpcBody(body);
  const inputJsonFromBody =
    typeof request.body === "string" ? request.body : undefined;

  return routerInstance.dispatch(
    {
      path: requestPath,
      input:
        request.input ??
        (inputFromQuery === undefined ? inputFromBody : undefined),
      inputJson:
        request.inputJson ??
        (inputFromQuery === undefined ? inputJsonFromBody : inputJsonFromQuery),
    },
    undefined,
    {
      method,
      context,
      ctx: request.ctx,
      headers,
      request,
    },
  );
}

export const handleTrpcRequest = handleRequest;

export async function handleFetchRequest(
  routerInstance: ProcedureRouter,
  request: Request,
  context: unknown = undefined,
): Promise<Response> {
  const method = request.method.toUpperCase();
  const url = request.url;
  const bodyText =
    method === "GET" || method === "HEAD" ? undefined : await request.text();
  const result = await handleRequest(routerInstance, {
    method,
    url,
    body: bodyText,
    context,
    headers: Object.fromEntries(request.headers.entries()),
  });

  const status =
    result.ok === true
      ? 200
      : "error" in result && result.error !== undefined
        ? HTTP_STATUS_BY_CODE[result.error.code]
        : 200;

  return new Response(JSON.stringify(result), {
    status,
    headers: { "content-type": "application/json; charset=utf-8" },
  });
}

export const procedureRouter = createRouter;
export const createAppRouter = createRouter;
export const handle = handleRequest;

export function statusCodeForError(code: ErrorCode): number {
  return HTTP_STATUS_BY_CODE[code];
}

export function toHttpResponse(result: ProcedureResult | BatchResult): {
  status: number;
  body: ProcedureResult | BatchResult;
} {
  if (result.ok) return { status: 200, body: result };
  if ("error" in result && result.error !== undefined) {
    return { status: HTTP_STATUS_BY_CODE[result.error.code], body: result };
  }
  return { status: 500, body: result };
}

class StrictJsonParser {
  private index = 0;
  private readonly text: string;

  constructor(text: string) {
    this.text = text;
  }

  parse(): unknown {
    this.skipWhitespace();
    const value = this.parseValue();
    this.skipWhitespace();
    if (this.index !== this.text.length) this.fail();
    return value;
  }

  private fail(): never {
    throw new SyntaxError("Invalid JSON");
  }

  private skipWhitespace(): void {
    while (this.index < this.text.length) {
      const code = this.text.charCodeAt(this.index);
      if (code === 0x20 || code === 0x09 || code === 0x0a || code === 0x0d) {
        this.index += 1;
      } else {
        break;
      }
    }
  }

  private parseValue(): unknown {
    if (this.index >= this.text.length) this.fail();
    const character = this.text[this.index];

    if (character === "{") return this.parseObject();
    if (character === "[") return this.parseArray();
    if (character === '"') return this.parseString();
    if (character === "t") return this.parseLiteral("true", true);
    if (character === "f") return this.parseLiteral("false", false);
    if (character === "n") return this.parseLiteral("null", null);
    if (character === "-" || (character >= "0" && character <= "9")) {
      return this.parseNumber();
    }
    return this.fail();
  }

  private parseObject(): Record<string, unknown> {
    const object: Record<string, unknown> = {};
    const keys = new Set<string>();
    this.index += 1;
    this.skipWhitespace();

    if (this.text[this.index] === "}") {
      this.index += 1;
      return object;
    }

    while (true) {
      if (this.text[this.index] !== '"') this.fail();
      const key = this.parseString();
      if (keys.has(key)) {
        throw new RouterError(ERROR_CODES.BAD_REQUEST, "Duplicate JSON key");
      }
      keys.add(key);
      this.skipWhitespace();

      if (this.text[this.index] !== ":") this.fail();
      this.index += 1;
      this.skipWhitespace();

      const value = this.parseValue();
      Object.defineProperty(object, key, {
        configurable: true,
        enumerable: true,
        value,
        writable: true,
      });

      this.skipWhitespace();
      const separator = this.text[this.index];
      if (separator === "}") {
        this.index += 1;
        return object;
      }
      if (separator !== ",") this.fail();
      this.index += 1;
      this.skipWhitespace();
    }
  }

  private parseArray(): unknown[] {
    const array: unknown[] = [];
    this.index += 1;
    this.skipWhitespace();

    if (this.text[this.index] === "]") {
      this.index += 1;
      return array;
    }

    while (true) {
      array.push(this.parseValue());
      this.skipWhitespace();
      const separator = this.text[this.index];
      if (separator === "]") {
        this.index += 1;
        return array;
      }
      if (separator !== ",") this.fail();
      this.index += 1;
      this.skipWhitespace();
    }
  }

  private parseString(): string {
    let result = "";
    this.index += 1;

    while (this.index < this.text.length) {
      const character = this.text[this.index];
      const code = this.text.charCodeAt(this.index);

      if (character === '"') {
        this.index += 1;
        return result;
      }

      if (character === "\\") {
        this.index += 1;
        const escaped = this.text[this.index];
        if (escaped === undefined) this.fail();
        this.index += 1;

        if (escaped === '"' || escaped === "\\" || escaped === "/") {
          result += escaped;
        } else if (escaped === "b") {
          result += "\b";
        } else if (escaped === "f") {
          result += "\f";
        } else if (escaped === "n") {
          result += "\n";
        } else if (escaped === "r") {
          result += "\r";
        } else if (escaped === "t") {
          result += "\t";
        } else if (escaped === "u") {
          const hex = this.text.slice(this.index, this.index + 4);
          if (!/^[0-9a-fA-F]{4}$/.test(hex)) this.fail();
          result += String.fromCharCode(parseInt(hex, 16));
          this.index += 4;
        } else {
          this.fail();
        }
        continue;
      }

      if (code < 0x20) this.fail();
      result += character;
      this.index += 1;
    }

    return this.fail();
  }

  private parseLiteral(literal: string, value: unknown): unknown {
    if (this.text.slice(this.index, this.index + literal.length) !== literal) {
      this.fail();
    }
    this.index += literal.length;
    return value;
  }

  private parseNumber(): number {
    const start = this.index;
    if (this.text[this.index] === "-") this.index += 1;

    if (this.text[this.index] === "0") {
      this.index += 1;
    } else {
      const first = this.text[this.index];
      if (first === undefined || first < "1" || first > "9") this.fail();
      this.index += 1;
      while (
        this.text[this.index] >= "0" &&
        this.text[this.index] <= "9"
      ) {
        this.index += 1;
      }
    }

    if (this.text[this.index] === ".") {
      this.index += 1;
      const firstFraction = this.text[this.index];
      if (
        firstFraction === undefined ||
        firstFraction < "0" ||
        firstFraction > "9"
      ) {
        this.fail();
      }
      while (
        this.text[this.index] >= "0" &&
        this.text[this.index] <= "9"
      ) {
        this.index += 1;
      }
    }

    if (this.text[this.index] === "e" || this.text[this.index] === "E") {
      this.index += 1;
      if (this.text[this.index] === "+" || this.text[this.index] === "-") {
        this.index += 1;
      }
      const firstExponent = this.text[this.index];
      if (
        firstExponent === undefined ||
        firstExponent < "0" ||
        firstExponent > "9"
      ) {
        this.fail();
      }
      while (
        this.text[this.index] >= "0" &&
        this.text[this.index] <= "9"
      ) {
        this.index += 1;
      }
    }

    return Number(this.text.slice(start, this.index));
  }
}

export function parseJsonStrict<T = unknown>(text: string): T {
  if (typeof text !== "string") {
    throw new TypeError("Expected a JSON string");
  }
  return new StrictJsonParser(text).parse() as T;
}

export const parseStrictJson = parseJsonStrict;
export const parseJson = parseJsonStrict;
export const strictJsonParse = parseJsonStrict;

const defaultExport = {
  BATCH_TOO_LARGE: ERROR_CODES.BATCH_TOO_LARGE,
  ERROR_CODES,
  MAX_BATCH_SIZE,
  ProcedureRouter,
  Router,
  createAppRouter,
  createProcedureRouter,
  createRouter,
  dispatch,
  dispatchBatch,
  dispatchProcedure,
  handle,
  handleFetchRequest,
  handleRequest,
  handleTrpcRequest,
  mutation,
  parseJson,
  parseJsonStrict,
  parseStrictJson,
  procedure,
  protectedProcedure,
  publicProcedure,
  query,
  router,
  statusCodeForError,
  strictJsonParse,
  subscription,
  toHttpResponse,
};

export default defaultExport;
