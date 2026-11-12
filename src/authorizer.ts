// RFC 8620 授权核心：请求级限制/能力预检，以及逐调用的
// 账户归属、读写权限、能力声明、结果引用校验与 id 二次授权。

import {
  resolveReferences,
  type ReferenceReplacement,
} from "./result-reference.ts";
import type {
  AccountId,
  AuthorizationOptions,
  CallDecision,
  ClientId,
  EntityId,
  JmapRequest,
  MethodCall,
  MethodResponse,
  RequestGate,
} from "./types.ts";

const CORE_CAPABILITY = "urn:ietf:params:jmap:core";
const DEFAULT_MAX_CALLS = 16;

function maxCallsLimit(options: AuthorizationOptions): number {
  if (options.maxCallsInRequest !== undefined) {
    return options.maxCallsInRequest;
  }
  const core = options.session.capabilities[CORE_CAPABILITY];
  const declared = core?.maxCallsInRequest;
  return typeof declared === "number" && declared > 0
    ? declared
    : DEFAULT_MAX_CALLS;
}

/**
 * 请求级预检（RFC 8620 §3.3 / §3.6.1）：
 * 结构错误 -> notRequest；using 含不支持能力 -> unknownCapability；
 * 调用数超限 -> limit。失败时整个请求拒绝，不进入逐调用处理。
 */
export function gateRequest(
  request: unknown,
  options: AuthorizationOptions,
): RequestGate {
  if (request === null || typeof request !== "object" || Array.isArray(request)) {
    return { ok: false, errorType: "notRequest", detail: "请求体不是 JSON 对象" };
  }
  const candidate = request as Record<string, unknown>;
  if (!Array.isArray(candidate.using) || !Array.isArray(candidate.methodCalls)) {
    return {
      ok: false,
      errorType: "notRequest",
      detail: "请求必须包含数组字段 using 和 methodCalls",
    };
  }

  const supported = options.session.capabilities;
  for (const capability of candidate.using as unknown[]) {
    if (typeof capability !== "string") {
      return {
        ok: false,
        errorType: "notRequest",
        detail: "using 中的能力 URN 必须是字符串",
      };
    }
    if (supported[capability] === undefined) {
      return {
        ok: false,
        errorType: "unknownCapability",
        detail: `服务器不支持 using 中声明的能力: ${capability}`,
      };
    }
  }

  const limit = maxCallsLimit(options);
  const calls = candidate.methodCalls as unknown[];
  if (calls.length > limit) {
    return {
      ok: false,
      errorType: "limit",
      detail:
        `methodCalls 数量 ${calls.length} 超过 maxCallsInRequest 上限 ${limit}`,
    };
  }

  return { ok: true };
}

function getPath(
  value: Record<string, unknown>,
  path: string,
): { found: boolean; value: unknown } {
  const segments = path.split(".");
  let current: unknown = value;
  for (const segment of segments) {
    if (current === null || typeof current !== "object") {
      return { found: false, value: undefined };
    }
    if (!Object.prototype.hasOwnProperty.call(current, segment)) {
      return { found: false, value: undefined };
    }
    current = (current as Record<string, unknown>)[segment];
  }
  return { found: true, value: current };
}

function collectIds(value: unknown): EntityId[] {
  if (typeof value === "string") {
    return [value];
  }
  if (Array.isArray(value)) {
    return value.filter((item): item is string => typeof item === "string");
  }
  return [];
}

export interface PreparedRequest {
  request: JmapRequest;
  limit: number;
}

/** 逐调用授权器：保持跨调用状态（成功调用的响应表）。 */
export class RequestAuthorizer {
  private readonly options: AuthorizationOptions;
  private readonly using: ReadonlySet<string>;
  private readonly responses = new Map<ClientId, MethodResponse>();

  private constructor(options: AuthorizationOptions, using: string[]) {
    this.options = options;
    this.using = new Set(using);
  }

  /** 工厂：调用方应先用 gateRequest 确认整请求通过预检。 */
  static create(
    request: JmapRequest,
    options: AuthorizationOptions,
  ): RequestAuthorizer {
    return new RequestAuthorizer(options, request.using);
  }

  /** 登记一个已成功执行的调用响应，供后续结果引用解析。 */
  recordSuccess(response: MethodResponse): void {
    this.responses.set(response[2], response);
  }

  /**
   * 授权单个方法调用。拒绝只影响该调用：调用方据此返回
   * method-level 错误后仍可继续授权并执行后续调用。
   */
  async authorize(call: MethodCall): Promise<CallDecision> {
    const [methodName, rawArgs, clientId] = call;
    const descriptor = this.options.methods[methodName];
    if (descriptor === undefined) {
      return {
        allowed: false,
        errorType: "unknownMethod",
        detail: `服务器未登记方法: ${methodName}`,
        methodName,
        clientId,
      };
    }

    // 1) 方法所属能力必须在 using 中声明（RFC 8620 §5.1）。
    if (!this.using.has(descriptor.capability)) {
      return {
        allowed: false,
        errorType: "unknownCapability",
        detail:
          `方法 ${methodName} 属于能力 ${descriptor.capability}，` +
          "但该能力未在请求 using 中声明",
        methodName,
        clientId,
      };
    }

    const requiresAccount = descriptor.requiresAccount !== false;
    const accountId = requiresAccount
      ? (rawArgs.accountId as AccountId | undefined)
      : undefined;

    // 2) 账户归属、账户能力与读写权限（RFC 8620 §6.1）。
    if (requiresAccount) {
      const accountDecision = this.checkAccount(
        methodName,
        clientId,
        descriptor.capability,
        descriptor.kind,
        accountId,
      );
      if (accountDecision !== null) {
        return accountDecision;
      }
    }

    // 3) 结果引用：必须指向同请求内更早且成功的调用。
    const resolution = resolveReferences(rawArgs, this.responses);
    if (!resolution.ok) {
      return {
        allowed: false,
        errorType: "invalidResultReference",
        detail: resolution.detail,
        methodName,
        clientId,
      };
    }

    // 4) 引用解出的 id 按被调用方的 accountId 重新授权。
    if (requiresAccount && descriptor.idBindings !== undefined) {
      const bindingDecision = await this.authorizeBindings({
        methodName,
        clientId,
        accountId: accountId as AccountId,
        kind: descriptor.kind,
        resolvedArgs: resolution.resolved,
        bindings: descriptor.idBindings,
        replacements: resolution.replacements,
      });
      if (bindingDecision !== null) {
        return bindingDecision;
      }
    }

    return {
      allowed: true,
      resolvedArgs: resolution.resolved,
      methodName,
      clientId,
      accountId,
      kind: descriptor.kind,
    };
  }

  private checkAccount(
    methodName: MethodCall[0],
    clientId: ClientId,
    capability: string,
    kind: "read" | "write",
    accountId: AccountId | undefined,
  ): CallDecision | null {
    const deny = (detail: string): CallDecision => ({
      allowed: false,
      errorType: "forbidden",
      detail,
      methodName,
      clientId,
    });

    if (typeof accountId !== "string" || accountId.length === 0) {
      return deny(`方法 ${methodName} 缺少有效的 accountId 参数`);
    }
    const account = this.options.session.accounts[accountId];
    if (account === undefined) {
      return deny(
        `accountId ${accountId} 不属于当前会话，无权访问他人账户`,
      );
    }
    const accountCapability = account.accountCapabilities[capability];
    if (accountCapability === undefined || accountCapability === false) {
      return deny(
        `账户 ${accountId} 不具备方法所需能力 ${capability}`,
      );
    }
    if (kind === "write" && account.isReadOnly === true) {
      return deny(`账户 ${accountId} 为只读账户，禁止写方法 ${methodName}`);
    }
    return null;
  }

  private async authorizeBindings(input: {
    methodName: MethodCall[0];
    clientId: ClientId;
    accountId: AccountId;
    kind: "read" | "write";
    resolvedArgs: Record<string, unknown>;
    bindings: NonNullable<
      NonNullable<AuthorizationOptions["methods"]>[string]["idBindings"]
    >;
    replacements: ReferenceReplacement[];
  }): Promise<CallDecision | null> {
    const { methodName, clientId, accountId, kind, resolvedArgs, bindings } =
      input;
    const checker = this.options.idAccessChecker;

    for (const binding of bindings) {
      const located = getPath(resolvedArgs, binding.path);
      if (!located.found) {
        // 可选参数未提供时无需授权。
        continue;
      }
      const entityIds = collectIds(located.value);
      if (entityIds.length === 0) {
        continue;
      }
      if (binding.multiple !== true && entityIds.length > 1) {
        return {
          allowed: false,
          errorType: "invalidResultReference",
          detail: `路径 ${binding.path} 期望单个 id，但解出了多个值`,
          methodName,
          clientId,
        };
      }
      // 该位置的值必须来自结果引用（题目要求的跨账户引用防线）；
      // 普通字面 id 由执行器自身的参数校验负责，这里不重复处理。
      const cameFromReference = input.replacements.some(
        (replacement) =>
          collectIds(replacement.value).length > 0 &&
          entityIds.some((id) =>
            collectIds(replacement.value).includes(id),
          ),
      );
      if (!cameFromReference) {
        continue;
      }
      if (checker === undefined) {
        return {
          allowed: false,
          errorType: "forbidden",
          detail:
            `方法 ${methodName} 的引用 id 需要二次授权，` +
            "但分发器未提供 idAccessChecker",
          methodName,
          clientId,
        };
      }
      const granted = new Set(
        await checker({
          accountId,
          entityType: binding.entityType,
          kind,
          entityIds,
        }),
      );
      const leaked = entityIds.filter((id) => !granted.has(id));
      if (leaked.length > 0) {
        return {
          allowed: false,
          errorType: "forbidden",
          detail:
            `结果引用解出的 ${binding.entityType} id ` +
            `${leaked.join(", ")} 不属于被调用方账户 ${accountId}，` +
            "跨账户引用被拒绝",
          methodName,
          clientId,
        };
      }
    }
    return null;
  }
}

/** 便捷工厂：先做请求级预检，通过后返回可逐调用使用的授权器。 */
export function prepareAuthorizedRequest(
  request: unknown,
  options: AuthorizationOptions,
):
  | { gate: Extract<RequestGate, { ok: false }> }
  | { gate: Extract<RequestGate, { ok: true }>; authorizer: RequestAuthorizer } {
  const gate = gateRequest(request, options);
  if (!gate.ok) {
    return { gate };
  }
  return {
    gate,
    authorizer: RequestAuthorizer.create(request as JmapRequest, options),
  };
}
