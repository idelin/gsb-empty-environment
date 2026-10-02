/** 服务器标识，例如 "A" / "B" / "payment-svc"。 */
export type ServerId = string;

/** 事件类型：消息的发送端或接收端。 */
export type EventKind = 'send' | 'recv';

/**
 * 离散追踪日志中的一条事件记录。
 * localTimeMs 是该服务器物理时钟读数（可能带未知漂移）。
 */
export interface TraceEvent {
  id: string;
  server: ServerId;
  kind: EventKind;
  /** 所属消息（一次单向传输）的标识。 */
  messageId: string;
  /** 本机物理时钟时间戳，单位毫秒。 */
  localTimeMs: number;
  /** 可选的人类可读标签，如 "A -> B: GetUser req"。 */
  label?: string;
}

/** 一条单向消息：由某服务器上的 send 事件与另一服务器上的 recv 事件构成。 */
export interface Message {
  id: string;
  from: ServerId;
  to: ServerId;
  sendEventId: string;
  recvEventId: string;
}

/** 重构器的输入：一批事件 + 消息配对关系。 */
export interface DistributedTrace {
  events: TraceEvent[];
  messages: Message[];
}

/**
 * RPC 请求/响应四元组（高层输入形式）：
 * client 在 clientSendMs 发请求、clientRecvMs 收响应；
 * server 在 serverRecvMs 收请求、serverSendMs 发响应。
 * 全部为各自服务器的本地物理时钟读数。
 */
export interface RpcQuadruple {
  rpcId: string;
  client: ServerId;
  server: ServerId;
  clientSendMs: number;
  serverRecvMs: number;
  serverSendMs: number;
  clientRecvMs: number;
  label?: string;
}

/** 时钟校正后的事件。 */
export interface CorrectedEvent extends TraceEvent {
  /** 校正后时间 = localTimeMs + offset[server]。 */
  correctedTimeMs: number;
}

/** 校正前发现的物理荒谬（负延迟）反例。 */
export interface LatencyViolation {
  messageId: string;
  from: ServerId;
  to: ServerId;
  /** 校正前的原始（物理）延迟，负值即“时钟倒流”。 */
  rawLatencyMs: number;
}

/** 因果图中的一条有向边。 */
export interface CausalEdge {
  fromEventId: string;
  toEventId: string;
  kind: 'message' | 'local-order';
}

/** 每条消息校正后的传输延迟。 */
export interface MessageLatency {
  messageId: string;
  from: ServerId;
  to: ServerId;
  rawLatencyMs: number;
  correctedLatencyMs: number;
}

/** 重构结果：物理自洽的因果拓扑。 */
export interface Reconstruction {
  /** 每台服务器推导出的时钟基线偏移（毫秒）。 */
  offsets: Record<ServerId, number>;
  /** 校正前检测到的负延迟反例。 */
  violations: LatencyViolation[];
  /** 校正后事件（含 correctedTimeMs）。 */
  events: CorrectedEvent[];
  /** 因果偏序图的边集。 */
  edges: CausalEdge[];
  /** 因果图的一个拓扑序（事件 id 列表）。 */
  topologicalOrder: string[];
  /** 每条消息的原始/校正延迟。 */
  messageLatencies: MessageLatency[];
}
