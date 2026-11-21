/**
 * JMAP 请求授权与分发（RFC 8620 §3 请求处理模型）。
 *
 * 安全规则：
 * 1. 调用数超过 maxCallsInRequest：整个请求以 limit 错误拒绝，不执行任何调用。
 * 2. using 中出现服务端不支持的能力：整个请求以 unknownCapability 拒绝（§3.3）。
 * 3. 逐调用隔离：任一调用授权失败只产出该调用的 error 响应，后续调用继续执行。
 * 4. 账户校验：accountId 必须属于当前会话，且该账户持有方法所需能力；
 *    写方法还要求账户非只读，否则该调用返回 forbidden（§3.4.2 / §6.1）。
 * 5. 方法所属能力必须在 using 中声明，否则该调用返回 unknownCapability。
 * 6. 结果引用必须指向同请求内更早且成功的调用，否则 invalidResultReference；
 *    引用解出的数据按被调用方的 accountId 重新授权，跨账户数据一律 forbidden。
 */

import { resolveReferences } from "./references.ts";
import { MethodError, RequestError, ResultReferenceError } from "./types.ts";
import type {
  CompletedCall,
  MethodCall,
  MethodDescriptor,
  MethodResponse,
  Session,
} from "./types.ts";

export interface AuthorizeAndDispatchOptions {
  /** 当前已认证会话。 */
  session: Session;
  /** 请求 using 数组中声明的能力标识。 */
  using: readonly string[];
  /** 请求 methodCalls 三元组数组。 */
  methodCalls: readonly MethodCall[];
  /** 分发器注册的方法表。 */
  methods: ReadonlyMap<string, MethodDescriptor>;
}

function errorResponse(
  callId: string,
  errorType: string,
  details: Record<string, unknown> = {},
): MethodResponse {
  return ["error", { type: errorType, ...details }, callId];
}

/**
 * 授权并依次分发一个 JMAP 请求的全部方法调用。
 * 请求级错误以 RequestError 抛出；调用级错误以 error 响应形式内联返回。
 */
export function authorizeAndDispatch(
  options: AuthorizeAndDispatchOptions,
): MethodResponse[] {
  const { session, using, methodCalls, methods } = options;

  // 规则 1：调用数上限为请求级限制，超限直接拒绝整个请求（RFC 8620 §3.4.1）。
  if (methodCalls.length > session.maxCallsInRequest) {
    throw new RequestError(
      "limit",
      `请求包含 ${methodCalls.length} 个方法调用，超过上限 ${session.maxCallsInRequest}`,
      {
        limit: "maxCallsInRequest",
        maxCallsInRequest: session.maxCallsInRequest,
        received: methodCalls.length,
      },
    );
  }

  // 规则 2：using 声明了服务端不具备的能力，整个请求 unknownCapability（§3.3）。
  const declaredCapabilities = new Set<string>();
  for (const capability of using) {
    if (!session.capabilities.has(capability)) {
      throw new RequestError(
        "unknownCapability",
        `服务端不支持 using 中声明的能力 ${capability}`,
        { capability },
      );
    }
    declaredCapabilities.add(capability);
  }

  const completed = new Map<string, CompletedCall>();
  const responses: MethodResponse[] = [];

  const recordFailure = (
    callId: string,
    methodName: string,
    accountId: string,
  ): void => {
    // 失败调用同样登记，使后续对它的引用能识别出“指向失败调用”。
    completed.set(callId, {
      callId,
      methodName,
      succeeded: false,
      responseArgs: {},
      accountId,
    });
  };

  for (const [methodName, rawArgs, callId] of methodCalls) {
    const requestedAccountId =
      typeof rawArgs.accountId === "string" ? rawArgs.accountId : "";
    const descriptor = methods.get(methodName);

    // 未注册方法：服务器不识别该方法名，按 unknownMethod 处理（§3.4.2）。
    if (descriptor === undefined) {
      responses.push(
        errorResponse(callId, "unknownMethod", { methodName }),
      );
      recordFailure(callId, methodName, requestedAccountId);
      continue;
    }

    // 规则 5：方法所属能力未在 using 中声明，仅该调用 unknownCapability。
    if (!declaredCapabilities.has(descriptor.capability)) {
      responses.push(
        errorResponse(callId, "unknownCapability", {
          methodName,
          requiredCapability: descriptor.capability,
        }),
      );
      recordFailure(callId, methodName, requestedAccountId);
      continue;
    }

    // 规则 4：accountId 必须是当前会话拥有的账户。
    const account = session.accounts.get(requestedAccountId);
    if (account === undefined) {
      responses.push(
        errorResponse(callId, "forbidden", {
          methodName,
          accountId: requestedAccountId,
          reason: "accountNotInSession",
        }),
      );
      recordFailure(callId, methodName, requestedAccountId);
      continue;
    }

    // 账户必须持有方法所属能力，且写方法要求账户非只读。
    if (!account.capabilities.has(descriptor.capability)) {
      responses.push(
        errorResponse(callId, "forbidden", {
          methodName,
          accountId: requestedAccountId,
          reason: "accountMissingCapability",
          requiredCapability: descriptor.capability,
        }),
      );
      recordFailure(callId, methodName, requestedAccountId);
      continue;
    }
    if (descriptor.access === "write" && account.isReadOnly) {
      responses.push(
        errorResponse(callId, "forbidden", {
          methodName,
          accountId: requestedAccountId,
          reason: "readOnlyAccount",
        }),
      );
      recordFailure(callId, methodName, requestedAccountId);
      continue;
    }

    // 规则 6a：解析本调用参数中的全部结果引用。
    let resolvedArgs: Record<string, unknown>;
    let referencedAccounts: ReadonlySet<string>;
    try {
      ({ resolvedArgs, referencedAccounts } = resolveReferences(
        rawArgs,
        completed,
      ));
    } catch (error) {
      if (error instanceof ResultReferenceError) {
        responses.push(
          errorResponse(callId, "invalidResultReference", error.details),
        );
        recordFailure(callId, methodName, requestedAccountId);
        continue;
      }
      throw error;
    }

    // 规则 6b：引用解出的数据按被调用方 accountId 重新授权。
    // 数据的来源账户与当前调用账户不一致即视为跨账户访问，拒绝该调用。
    let referenceRejected = false;
    for (const originAccountId of referencedAccounts) {
      if (originAccountId !== requestedAccountId) {
        responses.push(
          errorResponse(callId, "forbidden", {
            methodName,
            accountId: requestedAccountId,
            reason: "crossAccountResultReference",
            originAccountId,
          }),
        );
        recordFailure(callId, methodName, requestedAccountId);
        // 跨账户引用已确定非法，跳过后续授权与执行。
        referenceRejected = true;
        break;
      }
    }
    if (referenceRejected) {
      continue;
    }

    // 授权全部通过，执行方法处理器。
    try {
      const resultArgs = descriptor.handler(resolvedArgs);
      responses.push([methodName, resultArgs, callId]);
      completed.set(callId, {
        callId,
        methodName,
        succeeded: true,
        responseArgs: resultArgs,
        accountId: requestedAccountId,
      });
    } catch (error) {
      if (error instanceof MethodError) {
        responses.push(errorResponse(callId, error.errorType, error.details));
      } else {
        responses.push(
          errorResponse(callId, "serverFail", {
            message: error instanceof Error ? error.message : String(error),
          }),
        );
      }
      recordFailure(callId, methodName, requestedAccountId);
    }
  }

  return responses;
}
