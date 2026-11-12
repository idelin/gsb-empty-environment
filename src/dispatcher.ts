// 供请求分发器直接调用的端到端助手：预检 -> 逐调用授权 ->
// 执行成功调用 -> 组装 RFC 8620 §3.4 响应。

import { prepareAuthorizedRequest, RequestAuthorizer } from "./authorizer.ts";
import type {
  AuthorizationOptions,
  JmapRequest,
  MethodResponse,
  RequestErrorType,
} from "./types.ts";

export interface RequestLevelError {
  type: RequestErrorType;
  detail: string;
}

/** 执行器：仅对通过授权的调用触发，返回方法响应参数。 */
export type MethodExecutor = (input: {
  methodName: string;
  args: Record<string, unknown>;
  clientId: string;
  accountId?: string;
  kind: "read" | "write";
}) => Record<string, unknown> | Promise<Record<string, unknown>>;

export type DispatchOutcome =
  | {
      rejected: true;
      error: RequestLevelError;
    }
  | {
      rejected: false;
      methodResponses: MethodResponse[];
    };

// 运行完整授权-分发流程。授权被拒的调用只产生 method-level
// 错误响应且不执行，后续调用照常处理。
export async function dispatchAuthorized(
  request: unknown,
  options: AuthorizationOptions,
  execute: MethodExecutor,
): Promise<DispatchOutcome> {
  const prepared = prepareAuthorizedRequest(request, options);
  if (!prepared.gate.ok) {
    return {
      rejected: true,
      error: {
        type: prepared.gate.errorType,
        detail: prepared.gate.detail,
      },
    };
  }

  const authorizer: RequestAuthorizer = prepared.authorizer;
  const methodResponses: MethodResponse[] = [];
  for (const call of (request as JmapRequest).methodCalls) {
    const [methodName, , clientId] = call;
    const decision = await authorizer.authorize(call);
    if (!decision.allowed) {
      const errorResponse: MethodResponse = [
        "error",
        { type: decision.errorType, detail: decision.detail },
        clientId,
      ];
      methodResponses.push(errorResponse);
      continue;
    }

    const result = await execute({
      methodName: decision.methodName,
      args: decision.resolvedArgs,
      clientId: decision.clientId,
      accountId: decision.accountId,
      kind: decision.kind,
    });
    const successResponse: MethodResponse = [methodName, result, clientId];
    authorizer.recordSuccess(successResponse);
    methodResponses.push(successResponse);
  }

  return { rejected: false, methodResponses };
}
