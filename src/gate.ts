/**
 * 由协商出的子协议决定连接允许的消息类型。
 * 不允许的消息一收到就以关闭码 1008（Policy Violation）关闭连接。
 */

/** RFC 6455 数据帧种类；控制帧（ping/pong/close）由传输层处理，不走此策略。 */
export type FrameKind = "text" | "binary";

/** 单个子协议对应的消息策略。 */
export interface MessagePolicy {
  /** 允许的帧种类，例如 ["text"] 表示拒绝 binary。 */
  frameKinds: FrameKind[];
  /**
   * 文本消息内应用层 `type` 字段允许的取值。
   * 不配置则只按 frameKinds 判定；配置后未列出的 type 同样按 1008 拒绝。
   */
  appTypes?: string[];
}

export interface GateAllow {
  allowed: true;
  /** 解析出的应用层消息 type（若策略启用了 appTypes）。 */
  appType?: string;
}

export interface GateDeny {
  allowed: false;
  /** RFC 6455 §7.4.1：策略违反。 */
  closeCode: 1008;
  /** 可写日志的拒绝原因，不含消息内容。 */
  reason: string;
}

export type GateDecision = GateAllow | GateDeny;

function deny(reason: string): GateDeny {
  return { allowed: false, closeCode: 1008, reason };
}

/**
 * 判定一帧数据是否符合策略。
 *
 * @param kind 帧种类
 * @param payload 帧负载（text 为字符串，binary 为字节）
 * @param policy 该连接子协议绑定的策略
 * @param appTypeField 应用层类型字段名，默认 "type"
 */
export function decideFrame(
  kind: FrameKind,
  payload: string | Uint8Array,
  policy: MessagePolicy,
  appTypeField = "type",
): GateDecision {
  if (!policy.frameKinds.includes(kind)) {
    return deny(`frame kind "${kind}" is not allowed by the negotiated subprotocol`);
  }

  if (kind !== "text" || policy.appTypes === undefined) {
    return { allowed: true };
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(typeof payload === "string" ? payload : new TextDecoder().decode(payload));
  } catch {
    return deny("text frame is not valid JSON but appTypes policy is enforced");
  }

  if (typeof parsed !== "object" || parsed === null) {
    return deny("text frame payload must be a JSON object with an app type");
  }
  const appType = (parsed as Record<string, unknown>)[appTypeField];
  if (typeof appType !== "string" || !policy.appTypes.includes(appType)) {
    return deny(`app message type is not allowed by the negotiated subprotocol`);
  }
  return { allowed: true, appType };
}

/** 最小化的连接形状，ws 与内置集成层都满足该结构。 */
export interface CloseableLike {
  close(code?: number, reason?: string): void;
}

/**
 * 执行门控：允许时返回决策；拒绝时立即以 1008 关闭连接。
 * 适合直接在服务端的 message 事件回调里调用。
 */
export function enforceMessage(
  socket: CloseableLike,
  kind: FrameKind,
  payload: string | Uint8Array,
  policy: MessagePolicy,
  appTypeField = "type",
): GateDecision {
  const decision = decideFrame(kind, payload, policy, appTypeField);
  if (!decision.allowed) {
    socket.close(decision.closeCode, decision.reason);
  }
  return decision;
}
