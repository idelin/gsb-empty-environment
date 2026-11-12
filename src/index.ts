// JMAP 方法调用账户授权与结果引用授权包入口。

export {
  gateRequest,
  prepareAuthorizedRequest,
  RequestAuthorizer,
  type PreparedRequest,
} from "./authorizer.ts";
export { dispatchAuthorized } from "./dispatcher.ts";
export type {
  MethodExecutor,
  DispatchOutcome,
  RequestLevelError,
} from "./dispatcher.ts";
export {
  isResultReference,
  resolveReferences,
  type ReferenceReplacement,
  type ResultReference,
} from "./result-reference.ts";
export { JsonPointerError, parsePointer, resolvePointer } from "./json-pointer.ts";
export type {
  AccessKind,
  AccountId,
  AccountInfo,
  AuthorizationOptions,
  CallDecision,
  CapabilityId,
  ClientId,
  EntityId,
  IdAccessChecker,
  IdBinding,
  JmapRequest,
  MethodCall,
  MethodDescriptor,
  MethodErrorType,
  MethodName,
  MethodResponse,
  RequestErrorType,
  RequestGate,
  Session,
} from "./types.ts";
