/**
 * Batch bridge dispatcher for native-module calls.
 *
 * A batch is always represented by three parallel arrays:
 * [moduleIds[], methodIds[], params[]]. The dispatcher validates the batch
 * envelope as a whole, then validates and executes each call independently.
 *
 * Callback parameters are represented in the normalized handler arguments as
 * callable handles. Each handle also exposes an integer `id`; the same id is
 * available in `context.nativeParams` for code that forwards directly to a
 * native bridge. A callback may be invoked once. Later attempts are recorded
 * as security events and ignored.
 */

export type ParamKind =
  | "string"
  | "number"
  | "boolean"
  | "array"
  | "map"
  | "callback";

export interface ParamSpecObject {
  readonly type: ParamKind;
  /** Element spec for arrays, or an exact tuple when an array is used. */
  readonly items?: ParamSpec | readonly ParamSpec[];
  /** Value spec for maps. */
  readonly values?: ParamSpec;
  /** Key type for maps. String keys are the default for plain objects. */
  readonly keys?: "string" | "number";
  readonly optional?: boolean;
}

export type ParamSpec = ParamKind | ParamSpecObject;

export type DispatchErrorCode =
  | "INVALID_BATCH_INPUT"
  | "LENGTH_MISMATCH"
  | "INVALID_MODULE_ID"
  | "UNKNOWN_MODULE_ID"
  | "INVALID_METHOD_ID"
  | "UNKNOWN_METHOD_ID"
  | "INVALID_CALL_PARAMS"
  | "ARG_COUNT_MISMATCH"
  | "ARG_TYPE_MISMATCH"
  | "ARG_DEPTH_EXCEEDED"
  | "INVALID_CALLBACK_PARAM"
  | "CALLBACK_TOKEN_REPLAY"
  | "CYCLIC_VALUE"
  | "CALL_INTERNAL_ERROR"
  | "HANDLER_ERROR";

export type CallbackErrorCode =
  | "INVALID_CALLBACK_ID"
  | "UNKNOWN_CALLBACK_ID"
  | "CALLBACK_REPLAY"
  | "CALLBACK_ARG_DEPTH_EXCEEDED"
  | "CALLBACK_ARG_INVALID"
  | "CALLBACK_SINK_ERROR";

export interface DispatchIssue {
  readonly code: DispatchErrorCode;
  readonly message: string;
  /** -1 for a batch-level issue, otherwise the call index. */
  readonly index: number;
  readonly moduleId?: number;
  readonly methodId?: number;
  readonly path?: string;
}

export type CallbackTriggerResult =
  | {
      readonly ok: true;
      readonly callbackId: number;
      readonly invocationCount: number;
    }
  | {
      readonly ok: false;
      readonly callbackId: number;
      readonly code: CallbackErrorCode;
      readonly message: string;
    };

export interface CallbackHandle {
  readonly id: number;
  (...args: unknown[]): CallbackTriggerResult;
}

export interface CallbackEvent {
  readonly sequence: number;
  readonly callbackId: number;
  readonly callIndex: number;
  readonly moduleId: number;
  readonly methodId: number;
  readonly argumentIndex: number;
  readonly attempt: number;
  readonly accepted: boolean;
  readonly argsCount: number;
  readonly code?: CallbackErrorCode;
}

export interface SecurityEvent {
  readonly sequence: number;
  readonly code: string;
  readonly message: string;
  readonly callIndex?: number;
  readonly moduleId?: number;
  readonly methodId?: number;
  readonly callbackId?: number;
}

export interface MethodCallContext {
  readonly index: number;
  readonly moduleId: number;
  readonly methodId: number;
  /** Handler arguments with callback slots replaced by callable handles. */
  readonly params: readonly unknown[];
  /** Same arguments, with callback slots replaced by issued integer ids. */
  readonly nativeParams: readonly unknown[];
  /** Raw arguments exactly as submitted in the batch. */
  readonly rawParams: readonly unknown[];
  readonly callbackIds: readonly number[];
  readonly invokeCallback: (
    callbackId: number,
    ...args: unknown[]
  ) => CallbackTriggerResult;
}

export type MethodHandler = (
  params: readonly unknown[],
  context: MethodCallContext,
) => unknown;

export interface MethodDefinition {
  readonly params?: readonly ParamSpec[];
  readonly signature?: readonly ParamSpec[];
  readonly types?: readonly ParamSpec[];
  /** Count-only fallback for methods whose value types are not specified. */
  readonly arity?: number;
  readonly handler: MethodHandler;
}

export type MethodRegistry =
  | ReadonlyMap<number, MethodDefinition>
  | Record<number, MethodDefinition>
  | readonly (MethodDefinition & { readonly id?: number })[];

export interface ModuleDefinition {
  readonly id?: number;
  readonly moduleId?: number;
  readonly methods: MethodRegistry;
}

export type ModuleRegistry =
  | ReadonlyMap<number, ModuleDefinition>
  | Record<number, ModuleDefinition>
  | readonly ModuleDefinition[];

export type CallbackSink = (
  callbackId: number,
  args: readonly unknown[],
  context: {
    readonly callIndex: number;
    readonly moduleId: number;
    readonly methodId: number;
    readonly argumentIndex: number;
  },
) => unknown;

export interface BatchDispatcherOptions {
  readonly registry?: ModuleRegistry;
  readonly modules?: ModuleRegistry;
  /** Hard-capped at 8, as required by the bridge contract. */
  readonly maxDepth?: number;
  readonly callbackSink?: CallbackSink;
  readonly logger?: {
    readonly warn?: (message: string, event: SecurityEvent) => void;
  };
  readonly onSecurityEvent?: (event: SecurityEvent) => void;
}

export interface CallSuccessResult {
  readonly index: number;
  readonly moduleId: number;
  readonly methodId: number;
  readonly ok: true;
  readonly value: unknown;
  readonly callbackIds: readonly number[];
}

export interface CallFailureResult {
  readonly index: number;
  readonly moduleId?: number;
  readonly methodId?: number;
  readonly ok: false;
  readonly error: DispatchIssue;
}

export type CallResult = CallSuccessResult | CallFailureResult;

export interface BatchDispatchResult {
  /** False when the batch envelope is rejected or any individual call fails. */
  readonly ok: boolean;
  /** False only when the three-array batch envelope is invalid. */
  readonly accepted: boolean;
  readonly results: readonly CallResult[];
  readonly errors: readonly DispatchIssue[];
  readonly batchError?: DispatchIssue;
  readonly callbackEvents: readonly CallbackEvent[];
  readonly securityEvents: readonly SecurityEvent[];
}

type NormalizedParamKind = ParamKind | "any";

interface NormalizedParamSpec {
  readonly type: NormalizedParamKind;
  readonly optional: boolean;
  readonly items?: NormalizedParamSpec | readonly NormalizedParamSpec[];
  readonly values?: NormalizedParamSpec;
  readonly keys?: "string" | "number";
}

interface RegisteredMethod {
  readonly moduleId: number;
  readonly methodId: number;
  readonly signature: readonly NormalizedParamSpec[];
  readonly handler: MethodHandler;
}

interface CallbackRecord {
  readonly id: number;
  readonly callIndex: number;
  readonly moduleId: number;
  readonly methodId: number;
  readonly argumentIndex: number;
  readonly source: unknown;
  attempts: number;
  consumed: boolean;
}

const HARD_MAX_DEPTH = 8;
const PARAM_KINDS = new Set<string>([
  "string",
  "number",
  "boolean",
  "array",
  "map",
  "callback",
]);

function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}

function isModuleRegistry(value: unknown): value is ModuleRegistry {
  if (value instanceof Map || Array.isArray(value)) {
    return true;
  }
  if (!isPlainObject(value)) {
    return false;
  }
  const keys = Object.keys(value);
  if (keys.length === 0) {
    return true;
  }
  return !keys.some((key) =>
    ["registry", "modules", "maxDepth", "callbackSink", "logger", "onSecurityEvent"]
      .includes(key),
  );
}

function issue(
  code: DispatchErrorCode,
  message: string,
  index: number,
  extras: Omit<DispatchIssue, "code" | "message" | "index"> = {},
): DispatchIssue {
  return { code, message, index, ...extras };
}

function isNonNegativeSafeInteger(value: unknown): value is number {
  return (
    typeof value === "number" &&
    Number.isSafeInteger(value) &&
    value >= 0
  );
}

function normalizeSpec(
  spec: ParamSpec | "any",
  path: string,
  depth = 0,
): NormalizedParamSpec {
  if (depth > HARD_MAX_DEPTH) {
    throw new TypeError(`Parameter signature at ${path} exceeds depth 8`);
  }

  if (typeof spec === "string") {
    if (spec === "any") {
      return { type: "any", optional: false };
    }
    if (!PARAM_KINDS.has(spec)) {
      throw new TypeError(`Unknown parameter type "${spec}" at ${path}`);
    }
    return { type: spec as ParamKind, optional: false };
  }

  if (!isPlainObject(spec)) {
    throw new TypeError(`Invalid parameter signature at ${path}`);
  }
  const objectSpec = spec as ParamSpecObject;
  if (!PARAM_KINDS.has(objectSpec.type)) {
    throw new TypeError(`Unknown parameter type at ${path}`);
  }

  const type = objectSpec.type;
  const optional = objectSpec.optional === true;
  if (type !== "array" && objectSpec.items !== undefined) {
    throw new TypeError(`"items" is only valid for array params at ${path}`);
  }
  if (
    type !== "map" &&
    (objectSpec.values !== undefined || objectSpec.keys !== undefined)
  ) {
    throw new TypeError(`"values"/"keys" are only valid for map params at ${path}`);
  }

  let items: NormalizedParamSpec | readonly NormalizedParamSpec[] | undefined;
  if (objectSpec.items !== undefined) {
    if (Array.isArray(objectSpec.items)) {
      items = objectSpec.items.map((item, itemIndex) =>
        normalizeSpec(item, `${path}[${itemIndex}]`, depth + 1),
      );
    } else {
      items = normalizeSpec(objectSpec.items, `${path}[]`, depth + 1);
    }
  }

  const values =
    objectSpec.values === undefined
      ? undefined
      : normalizeSpec(objectSpec.values, `${path}{}`, depth + 1);

  return {
    type,
    optional,
    items,
    values,
    keys: objectSpec.keys,
  };
}

function normalizeSignature(definition: unknown, moduleId: number, methodId: number) {
  let rawSignature: unknown;
  let handler: unknown;

  if (typeof definition === "function") {
    handler = definition;
    rawSignature = (definition as MethodHandler & { signature?: unknown }).signature;
  } else if (Array.isArray(definition)) {
    rawSignature = definition[0];
    handler = definition[1];
  } else if (isPlainObject(definition)) {
    const methodDefinition = definition as unknown as MethodDefinition;
    handler = methodDefinition.handler;
    rawSignature =
      methodDefinition.params ??
      methodDefinition.signature ??
      methodDefinition.types;
    if (rawSignature === undefined && methodDefinition.arity !== undefined) {
      if (!isNonNegativeSafeInteger(methodDefinition.arity)) {
        throw new TypeError(
          `Invalid arity for method ${moduleId}:${methodId}`,
        );
      }
      rawSignature = Array.from({ length: methodDefinition.arity }, () => "any");
    }
    rawSignature ??= [];
  }

  if (typeof handler !== "function") {
    throw new TypeError(`Method ${moduleId}:${methodId} has no handler`);
  }
  if (!Array.isArray(rawSignature)) {
    throw new TypeError(`Method ${moduleId}:${methodId} signature must be an array`);
  }

  return {
    signature: rawSignature.map((spec, index) =>
      normalizeSpec(spec as ParamSpec, `method ${moduleId}:${methodId}[${index}]`),
    ),
    handler: handler as MethodHandler,
  };
}

function normalizeMethodMap(methodMap: MethodRegistry): Map<number, unknown> {
  const result = new Map<number, unknown>();
  if (methodMap instanceof Map) {
    for (const [id, definition] of methodMap) {
      result.set(id, definition);
    }
    return result;
  }
  if (Array.isArray(methodMap)) {
    methodMap.forEach((definition, arrayIndex) => {
      const id = (definition as { id?: unknown }).id ?? arrayIndex;
      result.set(id as number, definition);
    });
    return result;
  }
  if (!isPlainObject(methodMap)) {
    throw new TypeError("Module methods must be a map, object, or array");
  }
  for (const [key, definition] of Object.entries(methodMap)) {
    result.set(Number(key), definition);
  }
  return result;
}

/**
 * A reusable dispatcher. Registrations are immutable after registration;
 * each batch and each callback is fully isolated by its own record.
 */
export class BatchDispatcher {
  private readonly modules = new Map<number, Map<number, RegisteredMethod>>();
  private readonly callbackRecords = new Map<number, CallbackRecord>();
  private readonly usedCallbackTokens = new Set<number>();
  private readonly usedCallbackSources = new WeakSet<object>();
  private readonly callbackEvents: CallbackEvent[] = [];
  private readonly securityEvents: SecurityEvent[] = [];
  private readonly maxDepth: number;
  private readonly callbackSink?: CallbackSink;
  private readonly logger?: BatchDispatcherOptions["logger"];
  private readonly onSecurityEvent?: (event: SecurityEvent) => void;
  private nextCallbackId = 1;
  private nextSequence = 1;

  constructor(
    optionsOrRegistry: BatchDispatcherOptions | ModuleRegistry = {},
  ) {
    let options: BatchDispatcherOptions = {};
    let registry: ModuleRegistry | undefined;

    if (isModuleRegistry(optionsOrRegistry)) {
      registry = optionsOrRegistry;
    } else {
      options = optionsOrRegistry as BatchDispatcherOptions;
      registry = options.registry ?? options.modules;
    }

    const requestedDepth = options.maxDepth ?? HARD_MAX_DEPTH;
    if (!Number.isSafeInteger(requestedDepth) || requestedDepth < 1) {
      throw new TypeError("maxDepth must be a positive integer");
    }
    this.maxDepth = Math.min(requestedDepth, HARD_MAX_DEPTH);
    this.callbackSink = options.callbackSink;
    this.logger = options.logger;
    this.onSecurityEvent = options.onSecurityEvent;

    if (registry !== undefined) {
      this.loadRegistry(registry);
    }
  }

  registerModule(moduleId: number, methods: MethodRegistry): this {
    if (!isNonNegativeSafeInteger(moduleId)) {
      throw new TypeError("moduleId must be a non-negative safe integer");
    }
    const methodMap = normalizeMethodMap(methods);
    for (const [methodId, definition] of methodMap) {
      this.registerMethod(moduleId, methodId, definition as MethodDefinition);
    }
    return this;
  }

  registerMethod(
    moduleId: number,
    methodId: number,
    definition: MethodDefinition | MethodHandler,
  ): this {
    if (!isNonNegativeSafeInteger(moduleId)) {
      throw new TypeError("moduleId must be a non-negative safe integer");
    }
    if (!isNonNegativeSafeInteger(methodId)) {
      throw new TypeError("methodId must be a non-negative safe integer");
    }

    const normalized = normalizeSignature(definition, moduleId, methodId);
    let moduleMethods = this.modules.get(moduleId);
    if (moduleMethods === undefined) {
      moduleMethods = new Map();
      this.modules.set(moduleId, moduleMethods);
    }
    moduleMethods.set(methodId, {
      moduleId,
      methodId,
      signature: normalized.signature,
      handler: normalized.handler,
    });
    return this;
  }

  private loadRegistry(registry: ModuleRegistry): void {
    if (registry instanceof Map) {
      for (const [moduleId, definition] of registry) {
        this.registerModule(moduleId, definition.methods);
      }
      return;
    }
    if (Array.isArray(registry)) {
      registry.forEach((definition, arrayIndex) => {
        const moduleId = definition.id ?? definition.moduleId ?? arrayIndex;
        this.registerModule(moduleId, definition.methods);
      });
      return;
    }
    for (const [key, definition] of Object.entries(registry)) {
      this.registerModule(Number(key), definition.methods);
    }
  }

  dispatch(moduleIds: unknown, methodIds: unknown, params: unknown): BatchDispatchResult {
    return this.dispatchBatch(moduleIds, methodIds, params);
  }

  dispatchBatch(
    moduleIds: unknown,
    methodIds: unknown,
    params: unknown,
  ): BatchDispatchResult {
    const callbackEventStart = this.callbackEvents.length;
    const securityEventStart = this.securityEvents.length;
    let batchIssue: DispatchIssue | undefined;
    try {
      batchIssue = this.validateEnvelope(moduleIds, methodIds, params);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      batchIssue = issue(
        "INVALID_BATCH_INPUT",
        `Batch envelope could not be inspected: ${message}`,
        -1,
      );
    }

    if (batchIssue !== undefined) {
      this.recordSecurity(batchIssue.code, batchIssue.message, {
        callIndex: batchIssue.index,
      });
      return {
        ok: false,
        accepted: false,
        results: [],
        errors: [batchIssue],
        batchError: batchIssue,
        callbackEvents: this.callbackEvents.slice(callbackEventStart),
        securityEvents: this.securityEvents.slice(securityEventStart),
      };
    }

    const typedModuleIds = moduleIds as readonly unknown[];
    const typedMethodIds = methodIds as readonly unknown[];
    const typedParams = params as readonly unknown[];
    const results: CallResult[] = [];

    for (let index = 0; index < typedModuleIds.length; index += 1) {
      try {
        results.push(
          this.dispatchCall(
            index,
            typedModuleIds[index],
            typedMethodIds[index],
            typedParams[index],
          ),
        );
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        const callError = issue(
          "CALL_INTERNAL_ERROR",
          `Call at index ${index} failed during validation: ${message}`,
          index,
        );
        this.recordSecurity(callError.code, callError.message, {
          callIndex: index,
        });
        results.push({ index, ok: false, error: callError });
      }
    }

    const errors = results.flatMap((result) =>
      result.ok ? [] : [result.error],
    );
    return {
      ok: errors.length === 0,
      accepted: true,
      results,
      errors,
      callbackEvents: this.callbackEvents.slice(callbackEventStart),
      securityEvents: this.securityEvents.slice(securityEventStart),
    };
  }

  /**
   * Convenience wrapper that turns only envelope rejection (invalid arrays or
   * unequal lengths) into `BatchRejectedError`. Per-call failures remain in
   * the returned result.
   */
  dispatchBatchStrict(
    moduleIds: unknown,
    methodIds: unknown,
    params: unknown,
  ): BatchDispatchResult {
    return assertBatchEnvelope(this.dispatchBatch(moduleIds, methodIds, params));
  }

  private validateEnvelope(
    moduleIds: unknown,
    methodIds: unknown,
    params: unknown,
  ): DispatchIssue | undefined {
    if (!Array.isArray(moduleIds) || !Array.isArray(methodIds) || !Array.isArray(params)) {
      return issue(
        "INVALID_BATCH_INPUT",
        "moduleIds, methodIds and params must all be arrays",
        -1,
      );
    }
    if (
      moduleIds.length !== methodIds.length ||
      moduleIds.length !== params.length
    ) {
      return issue(
        "LENGTH_MISMATCH",
        `Batch arrays must have equal length (moduleIds=${moduleIds.length}, ` +
          `methodIds=${methodIds.length}, params=${params.length})`,
        -1,
      );
    }
    return undefined;
  }

  private dispatchCall(
    index: number,
    rawModuleId: unknown,
    rawMethodId: unknown,
    rawParams: unknown,
  ): CallResult {
    if (!isNonNegativeSafeInteger(rawModuleId)) {
      const error = issue(
        "INVALID_MODULE_ID",
        `moduleId at index ${index} must be a non-negative safe integer`,
        index,
      );
      this.recordSecurity(error.code, error.message, { callIndex: index });
      return { index, ok: false, error };
    }
    const moduleMethods = this.modules.get(rawModuleId);
    if (moduleMethods === undefined) {
      const error = issue(
        "UNKNOWN_MODULE_ID",
        `moduleId ${rawModuleId} at index ${index} is not registered`,
        index,
        { moduleId: rawModuleId },
      );
      this.recordSecurity(error.code, error.message, {
        callIndex: index,
        moduleId: rawModuleId,
      });
      return { index, moduleId: rawModuleId, ok: false, error };
    }

    if (!isNonNegativeSafeInteger(rawMethodId)) {
      const error = issue(
        "INVALID_METHOD_ID",
        `methodId at index ${index} must be a non-negative safe integer`,
        index,
        { moduleId: rawModuleId },
      );
      this.recordSecurity(error.code, error.message, {
        callIndex: index,
        moduleId: rawModuleId,
      });
      return { index, moduleId: rawModuleId, ok: false, error };
    }
    const method = moduleMethods.get(rawMethodId);
    if (method === undefined) {
      const error = issue(
        "UNKNOWN_METHOD_ID",
        `methodId ${rawMethodId} at index ${index} is not registered for module ${rawModuleId}`,
        index,
        { moduleId: rawModuleId, methodId: rawMethodId },
      );
      this.recordSecurity(error.code, error.message, {
        callIndex: index,
        moduleId: rawModuleId,
        methodId: rawMethodId,
      });
      return {
        index,
        moduleId: rawModuleId,
        methodId: rawMethodId,
        ok: false,
        error,
      };
    }

    if (!Array.isArray(rawParams)) {
      const error = issue(
        "INVALID_CALL_PARAMS",
        `params[${index}] must be an array`,
        index,
        { moduleId: rawModuleId, methodId: rawMethodId },
      );
      this.recordSecurity(error.code, error.message, {
        callIndex: index,
        moduleId: rawModuleId,
        methodId: rawMethodId,
      });
      return {
        index,
        moduleId: rawModuleId,
        methodId: rawMethodId,
        ok: false,
        error,
      };
    }

    const validation = this.validateArguments(method, rawParams);
    if (validation.error !== undefined) {
      const error = { ...validation.error, index };
      this.recordSecurity(error.code, error.message, {
        callIndex: index,
        moduleId: rawModuleId,
        methodId: rawMethodId,
      });
      return {
        index,
        moduleId: rawModuleId,
        methodId: rawMethodId,
        ok: false,
        error,
      };
    }

    // Mark externally supplied callback tokens/sources only after the whole
    // call validates, so a malformed call cannot burn another call's token.
    for (const token of validation.callbackTokens) {
      this.usedCallbackTokens.add(token);
    }
    for (const source of validation.callbackSources) {
      this.usedCallbackSources.add(source);
    }

    const callbackIds: number[] = [];
    const normalizedParams: unknown[] = [];
    const nativeParams: unknown[] = [];

    for (let argumentIndex = 0; argumentIndex < rawParams.length; argumentIndex += 1) {
      const spec = method.signature[argumentIndex];
      if (spec?.type !== "callback") {
        normalizedParams.push(rawParams[argumentIndex]);
        nativeParams.push(rawParams[argumentIndex]);
        continue;
      }

      const record = this.createCallback(
        index,
        rawModuleId,
        rawMethodId,
        argumentIndex,
        rawParams[argumentIndex],
      );
      const handle = this.createCallbackHandle(record.id);
      callbackIds.push(record.id);
      normalizedParams.push(handle);
      nativeParams.push(record.id);
    }

    const context: MethodCallContext = {
      index,
      moduleId: rawModuleId,
      methodId: rawMethodId,
      params: normalizedParams,
      nativeParams,
      rawParams,
      callbackIds,
      invokeCallback: (callbackId, ...args) =>
        this.invokeCallback(callbackId, ...args),
    };

    try {
      const value = method.handler(normalizedParams, context);
      return {
        index,
        moduleId: rawModuleId,
        methodId: rawMethodId,
        ok: true,
        value,
        callbackIds,
      };
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      const callError = issue(
        "HANDLER_ERROR",
        `Handler for ${rawModuleId}:${rawMethodId} threw: ${message}`,
        index,
        { moduleId: rawModuleId, methodId: rawMethodId },
      );
      this.recordSecurity(callError.code, callError.message, {
        callIndex: index,
        moduleId: rawModuleId,
        methodId: rawMethodId,
      });
      return {
        index,
        moduleId: rawModuleId,
        methodId: rawMethodId,
        ok: false,
        error: callError,
      };
    }
  }

  private validateArguments(
    method: RegisteredMethod,
    rawParams: readonly unknown[],
  ): {
    readonly error?: DispatchIssue;
    readonly callbackTokens: readonly number[];
    readonly callbackSources: readonly object[];
  } {
    const required = method.signature.filter((spec) => !spec.optional).length;
    const max = method.signature.length;
    const missingRequiredPosition = method.signature.some(
      (spec, index) => !spec.optional && index >= rawParams.length,
    );
    if (missingRequiredPosition || rawParams.length > max) {
      return {
        error: issue(
          "ARG_COUNT_MISMATCH",
          `Method ${method.moduleId}:${method.methodId} expects ` +
            `${required === max ? max : `${required}-${max}`} argument(s), ` +
            `received ${rawParams.length}`,
          -1,
          { moduleId: method.moduleId, methodId: method.methodId },
        ),
        callbackTokens: [],
        callbackSources: [],
      };
    }

    const callbackTokens: number[] = [];
    const callbackSources: object[] = [];
    const pendingTokens = new Set<number>();
    const pendingSources = new WeakSet<object>();

    for (let index = 0; index < rawParams.length; index += 1) {
      const spec = method.signature[index];
      const value = rawParams[index];
      if (spec === undefined) {
        continue;
      }
      if (value === undefined && spec.optional) {
        continue;
      }

      if (spec.type === "callback") {
        const callbackError = this.validateCallbackArgument(
          value,
          index,
          pendingTokens,
          pendingSources,
        );
        if (callbackError !== undefined) {
          return {
            error: callbackError,
            callbackTokens: [],
            callbackSources: [],
          };
        }
        if (isNonNegativeSafeInteger(value)) {
          callbackTokens.push(value);
          pendingTokens.add(value);
        } else if (
          (typeof value === "function" || typeof value === "object") &&
          value !== null
        ) {
          callbackSources.push(value as object);
          pendingSources.add(value as object);
        }
        continue;
      }

      const error = this.validateValue(
        value,
        spec,
        0,
        `params[${index}]`,
        new WeakSet(),
      );
      if (error !== undefined) {
        return {
          error,
          callbackTokens: [],
          callbackSources: [],
        };
      }
    }

    return { callbackTokens, callbackSources };
  }

  private validateCallbackArgument(
    value: unknown,
    argumentIndex: number,
    pendingTokens: Set<number>,
    pendingSources: WeakSet<object>,
  ): DispatchIssue | undefined {
    if (value === undefined || value === null) {
      return undefined;
    }
    if (typeof value === "function") {
      if (
        this.usedCallbackSources.has(value) ||
        pendingSources.has(value)
      ) {
        return issue(
          "CALLBACK_TOKEN_REPLAY",
          `Callback source at argument ${argumentIndex} was already used`,
          -1,
        );
      }
      return undefined;
    }
    if (typeof value === "number" && Number.isSafeInteger(value)) {
      if (value < 0) {
        return issue(
          "INVALID_CALLBACK_PARAM",
          `Callback id at argument ${argumentIndex} must be a non-negative safe integer`,
          -1,
        );
      }
      if (this.usedCallbackTokens.has(value) || pendingTokens.has(value)) {
        return issue(
          "CALLBACK_TOKEN_REPLAY",
          `Callback id ${value} at argument ${argumentIndex} was already used`,
          -1,
        );
      }
      return undefined;
    }
    return issue(
      "INVALID_CALLBACK_PARAM",
      `Callback at argument ${argumentIndex} must be an issued id, ` +
        `a callback function, null, or undefined`,
      -1,
    );
  }

  private validateValue(
    value: unknown,
    spec: NormalizedParamSpec,
    depth: number,
    path: string,
    ancestors: WeakSet<object>,
  ): DispatchIssue | undefined {
    if (depth > this.maxDepth) {
      return issue(
        "ARG_DEPTH_EXCEEDED",
        `Value at ${path} exceeds the maximum nesting depth of ${this.maxDepth}`,
        -1,
        { path },
      );
    }

    switch (spec.type) {
      case "any":
        return this.validateUnspecifiedValue(value, depth, path, ancestors);
      case "string":
        return typeof value === "string"
          ? undefined
          : this.typeIssue(path, "string", value);
      case "number":
        return typeof value === "number" && Number.isFinite(value)
          ? undefined
          : this.typeIssue(path, "finite number", value);
      case "boolean":
        return typeof value === "boolean"
          ? undefined
          : this.typeIssue(path, "boolean", value);
      case "callback":
        return issue(
          "ARG_TYPE_MISMATCH",
          `Callback at ${path} cannot be nested`,
          -1,
          { path },
        );
      case "array":
        return this.validateArray(value, spec, depth, path, ancestors);
      case "map":
        return this.validateMap(value, spec, depth, path, ancestors);
    }
  }

  private validateArray(
    value: unknown,
    spec: NormalizedParamSpec,
    depth: number,
    path: string,
    ancestors: WeakSet<object>,
  ): DispatchIssue | undefined {
    if (!Array.isArray(value)) {
      return this.typeIssue(path, "array", value);
    }
    if (ancestors.has(value)) {
      return this.cyclicIssue(path);
    }
    ancestors.add(value);

    try {
      if (Array.isArray(spec.items)) {
        if (value.length !== spec.items.length) {
          return issue(
            "ARG_TYPE_MISMATCH",
            `${path} must contain exactly ${spec.items.length} item(s)`,
            -1,
            { path },
          );
        }
        for (let index = 0; index < value.length; index += 1) {
          const itemError = this.validateValue(
            value[index],
            spec.items[index],
            depth + 1,
            `${path}[${index}]`,
            ancestors,
          );
          if (itemError !== undefined) {
            return itemError;
          }
        }
        return undefined;
      }

      for (let index = 0; index < value.length; index += 1) {
        if (spec.items === undefined) {
          const genericError = this.validateUnspecifiedValue(
            value[index],
            depth + 1,
            `${path}[${index}]`,
            ancestors,
          );
          if (genericError !== undefined) {
            return genericError;
          }
          continue;
        }
        const itemError = this.validateValue(
          value[index],
          spec.items,
          depth + 1,
          `${path}[${index}]`,
          ancestors,
        );
        if (itemError !== undefined) {
          return itemError;
        }
      }
      return undefined;
    } finally {
      ancestors.delete(value);
    }
  }

  private validateMap(
    value: unknown,
    spec: NormalizedParamSpec,
    depth: number,
    path: string,
    ancestors: WeakSet<object>,
  ): DispatchIssue | undefined {
    const isMap = value instanceof Map;
    if (!isMap && !isPlainObject(value)) {
      return this.typeIssue(path, "plain object map", value);
    }
    const objectValue = value as object;
    if (ancestors.has(objectValue)) {
      return this.cyclicIssue(path);
    }
    ancestors.add(objectValue);

    try {
      if (isMap) {
        for (const [key, mapValue] of value as Map<unknown, unknown>) {
          const keyError = this.validateMapKey(key, spec.keys, path, true);
          if (keyError !== undefined) {
            return keyError;
          }
          const valueError = this.validateMapValue(
            mapValue,
            spec,
            depth,
            `${path}[${String(key)}]`,
            ancestors,
          );
          if (valueError !== undefined) {
            return valueError;
          }
        }
        return undefined;
      }

      for (const key of Object.keys(value as Record<string, unknown>)) {
        const keyError = this.validateMapKey(key, spec.keys, path, false);
        if (keyError !== undefined) {
          return keyError;
        }
        const valueError = this.validateMapValue(
          (value as Record<string, unknown>)[key],
          spec,
          depth,
          `${path}.${key}`,
          ancestors,
        );
        if (valueError !== undefined) {
          return valueError;
        }
      }
      return undefined;
    } finally {
      ancestors.delete(objectValue);
    }
  }

  private validateMapKey(
    key: unknown,
    expected: "string" | "number" | undefined,
    path: string,
    isMap: boolean,
  ): DispatchIssue | undefined {
    if (expected === "number") {
      return typeof key === "number" && Number.isFinite(key)
        ? undefined
        : issue(
            "ARG_TYPE_MISMATCH",
            `Map key in ${path} must be a finite number`,
            -1,
            { path },
          );
    }
    if (typeof key === "string") {
      return undefined;
    }
    if (isMap && typeof key === "number" && Number.isFinite(key)) {
      return undefined;
    }
    return issue(
      "ARG_TYPE_MISMATCH",
      `Map key in ${path} must be a string`,
      -1,
      { path },
    );
  }

  private validateMapValue(
    value: unknown,
    spec: NormalizedParamSpec,
    depth: number,
    path: string,
    ancestors: WeakSet<object>,
  ): DispatchIssue | undefined {
    if (spec.values !== undefined) {
      return this.validateValue(value, spec.values, depth + 1, path, ancestors);
    }
    return this.validateUnspecifiedValue(value, depth + 1, path, ancestors);
  }

  private validateUnspecifiedValue(
    value: unknown,
    depth: number,
    path: string,
    ancestors: WeakSet<object>,
  ): DispatchIssue | undefined {
    if (depth > this.maxDepth) {
      return issue(
        "ARG_DEPTH_EXCEEDED",
        `Value at ${path} exceeds the maximum nesting depth of ${this.maxDepth}`,
        -1,
        { path },
      );
    }
    if (typeof value === "string" || typeof value === "boolean") {
      return undefined;
    }
    if (typeof value === "number" && Number.isFinite(value)) {
      return undefined;
    }
    if (Array.isArray(value)) {
      if (ancestors.has(value)) {
        return this.cyclicIssue(path);
      }
      ancestors.add(value);
      try {
        for (let index = 0; index < value.length; index += 1) {
          const error = this.validateUnspecifiedValue(
            value[index],
            depth + 1,
            `${path}[${index}]`,
            ancestors,
          );
          if (error !== undefined) {
            return error;
          }
        }
        return undefined;
      } finally {
        ancestors.delete(value);
      }
    }
    if (isPlainObject(value)) {
      if (ancestors.has(value)) {
        return this.cyclicIssue(path);
      }
      ancestors.add(value);
      try {
        for (const [key, child] of Object.entries(value)) {
          const error = this.validateUnspecifiedValue(
            child,
            depth + 1,
            `${path}.${key}`,
            ancestors,
          );
          if (error !== undefined) {
            return error;
          }
        }
        return undefined;
      } finally {
        ancestors.delete(value);
      }
    }
    if (value instanceof Map) {
      if (ancestors.has(value)) {
        return this.cyclicIssue(path);
      }
      ancestors.add(value);
      try {
        for (const [key, child] of value) {
          if (
            typeof key !== "string" &&
            !(typeof key === "number" && Number.isFinite(key))
          ) {
            return issue(
              "ARG_TYPE_MISMATCH",
              `Map key in ${path} must be a string or finite number`,
              -1,
              { path },
            );
          }
          const error = this.validateUnspecifiedValue(
            child,
            depth + 1,
            `${path}[${String(key)}]`,
            ancestors,
          );
          if (error !== undefined) {
            return error;
          }
        }
        return undefined;
      } finally {
        ancestors.delete(value);
      }
    }
    return issue(
      "ARG_TYPE_MISMATCH",
      `Unsupported value at ${path}; expected string, number, boolean, array, or map`,
      -1,
      { path },
    );
  }

  private typeIssue(
    path: string,
    expected: string,
    value: unknown,
  ): DispatchIssue {
    let actual = typeof value;
    if (value === null) {
      actual = "null";
    } else if (Array.isArray(value)) {
      actual = "array";
    } else if (isPlainObject(value) || value instanceof Map) {
      actual = "map";
    }
    return issue(
      "ARG_TYPE_MISMATCH",
      `${path} must be a ${expected}, received ${actual}`,
      -1,
      { path },
    );
  }

  private cyclicIssue(path: string): DispatchIssue {
    return issue(
      "CYCLIC_VALUE",
      `${path} contains a cyclic reference`,
      -1,
      { path },
    );
  }

  private createCallback(
    callIndex: number,
    moduleId: number,
    methodId: number,
    argumentIndex: number,
    source: unknown,
  ): CallbackRecord {
    const id = this.nextCallbackId;
    if (!Number.isSafeInteger(id)) {
      throw new Error("Callback id space exhausted");
    }
    this.nextCallbackId += 1;

    const record: CallbackRecord = {
      id,
      callIndex,
      moduleId,
      methodId,
      argumentIndex,
      source,
      attempts: 0,
      consumed: false,
    };
    this.callbackRecords.set(id, record);
    return record;
  }

  private createCallbackHandle(id: number): CallbackHandle {
    const handle = ((...args: unknown[]) =>
      this.invokeCallback(id, ...args)) as CallbackHandle;
    Object.defineProperty(handle, "id", {
      value: id,
      enumerable: true,
      configurable: false,
      writable: false,
    });
    return handle;
  }

  invokeCallback(
    callbackId: unknown,
    ...args: unknown[]
  ): CallbackTriggerResult {
    if (!isNonNegativeSafeInteger(callbackId)) {
      return {
        ok: false,
        callbackId: typeof callbackId === "number" ? callbackId : -1,
        code: "INVALID_CALLBACK_ID",
        message: "Callback id must be a non-negative safe integer",
      };
    }
    const record = this.callbackRecords.get(callbackId);
    if (record === undefined) {
      const message = `Callback id ${callbackId} was not issued by this dispatcher`;
      this.recordSecurity("UNKNOWN_CALLBACK_ID", message, { callbackId });
      return {
        ok: false,
        callbackId,
        code: "UNKNOWN_CALLBACK_ID",
        message,
      };
    }

    record.attempts += 1;
    if (record.consumed) {
      const message = `Callback ${callbackId} was already invoked; replay ignored`;
      this.recordCallbackEvent(record, false, args.length, "CALLBACK_REPLAY");
      this.recordSecurity("CALLBACK_REPLAY", message, {
        callIndex: record.callIndex,
        moduleId: record.moduleId,
        methodId: record.methodId,
        callbackId,
      });
      return {
        ok: false,
        callbackId,
        code: "CALLBACK_REPLAY",
        message,
      };
    }

    const argumentError = this.validateCallbackArguments(args);
    if (argumentError !== undefined) {
      this.recordCallbackEvent(
        record,
        false,
        args.length,
        argumentError.code,
      );
      this.recordSecurity(argumentError.code, argumentError.message, {
        callIndex: record.callIndex,
        moduleId: record.moduleId,
        methodId: record.methodId,
        callbackId,
      });
      return {
        ok: false,
        callbackId,
        code: argumentError.code,
        message: argumentError.message,
      };
    }

    record.consumed = true;
    this.recordCallbackEvent(record, true, args.length);

    try {
      this.invokeCallbackSink(record, args);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.recordSecurity("CALLBACK_SINK_ERROR", message, {
        callIndex: record.callIndex,
        moduleId: record.moduleId,
        methodId: record.methodId,
        callbackId,
      });
      return {
        ok: false,
        callbackId,
        code: "CALLBACK_SINK_ERROR",
        message,
      };
    }

    return {
      ok: true,
      callbackId,
      invocationCount: 1,
    };
  }

  private validateCallbackArguments(
    args: readonly unknown[],
  ): { code: CallbackErrorCode; message: string } | undefined {
    const ancestors = new WeakSet<object>();
    for (let index = 0; index < args.length; index += 1) {
      const error = this.validateUnspecifiedValue(
        args[index],
        0,
        `callbackArgs[${index}]`,
        ancestors,
      );
      if (error === undefined) {
        continue;
      }
      if (error.code === "ARG_DEPTH_EXCEEDED") {
        return {
          code: "CALLBACK_ARG_DEPTH_EXCEEDED",
          message: error.message,
        };
      }
      return {
        code: "CALLBACK_ARG_INVALID",
        message: error.message,
      };
    }
    return undefined;
  }

  private invokeCallbackSink(
    record: CallbackRecord,
    args: readonly unknown[],
  ): void {
    if (typeof record.source === "function") {
      (record.source as (...callbackArgs: unknown[]) => unknown)(...args);
      return;
    }
    if (this.callbackSink !== undefined) {
      this.callbackSink(record.id, args, {
        callIndex: record.callIndex,
        moduleId: record.moduleId,
        methodId: record.methodId,
        argumentIndex: record.argumentIndex,
      });
    }
  }

  private recordCallbackEvent(
    record: CallbackRecord,
    accepted: boolean,
    argsCount: number,
    code?: CallbackErrorCode,
  ): void {
    this.callbackEvents.push({
      sequence: this.nextSequence,
      callbackId: record.id,
      callIndex: record.callIndex,
      moduleId: record.moduleId,
      methodId: record.methodId,
      argumentIndex: record.argumentIndex,
      attempt: record.attempts,
      accepted,
      argsCount,
      ...(code === undefined ? {} : { code }),
    });
    this.nextSequence += 1;
  }

  private recordSecurity(
    code: string,
    message: string,
    extras: {
      readonly callIndex?: number;
      readonly moduleId?: number;
      readonly methodId?: number;
      readonly callbackId?: number;
    } = {},
  ): void {
    const event: SecurityEvent = {
      sequence: this.nextSequence,
      code,
      message,
      ...extras,
    };
    this.nextSequence += 1;
    this.securityEvents.push(event);
    try {
      this.logger?.warn?.(`[batch-bridge] ${code}: ${message}`, event);
    } catch {
      // Logging must never break dispatch.
    }
    try {
      this.onSecurityEvent?.(event);
    } catch {
      // Observer failures must never break dispatch.
    }
  }

  getCallbackEvents(): readonly CallbackEvent[] {
    return this.callbackEvents.slice();
  }

  getSecurityEvents(): readonly SecurityEvent[] {
    return this.securityEvents.slice();
  }

  getCallbackIdsForCall(callIndex: number): readonly number[] {
    const ids: number[] = [];
    for (const record of this.callbackRecords.values()) {
      if (record.callIndex === callIndex) {
        ids.push(record.id);
      }
    }
    return ids;
  }
}

export function createBatchDispatcher(
  optionsOrRegistry: BatchDispatcherOptions | ModuleRegistry = {},
): BatchDispatcher {
  return new BatchDispatcher(optionsOrRegistry);
}

export function dispatchBatch(
  dispatcher: BatchDispatcher,
  moduleIds: unknown,
  methodIds: unknown,
  params: unknown,
): BatchDispatchResult {
  if (!(dispatcher instanceof BatchDispatcher)) {
    throw new TypeError("dispatcher must be a BatchDispatcher");
  }
  return dispatcher.dispatchBatch(moduleIds, methodIds, params);
}

export class BatchRejectedError extends Error {
  readonly result: BatchDispatchResult;

  constructor(result: BatchDispatchResult) {
    super(
      result.batchError?.message ??
        result.errors[0]?.message ??
        "Batch was rejected",
    );
    this.name = "BatchRejectedError";
    this.result = result;
  }
}

/** Throws only when the batch envelope itself is rejected. */
export function assertBatchEnvelope(
  result: BatchDispatchResult,
): BatchDispatchResult {
  if (!result.accepted) {
    throw new BatchRejectedError(result);
  }
  return result;
}

export default BatchDispatcher;
