/** 订单事件：由上游以“至少一次”语义投递，可能重复。 */
export interface OrderEvent {
  eventId: string;
  orderId: string;
  status: string;
  amount: number;
}

/** 待发通知：与订单更新在同一事务中登记（事务性发件箱）。 */
export interface Notification {
  notificationId: string;
  orderId: string;
  eventId: string;
  payload: string;
}

export interface Order {
  orderId: string;
  status: string;
  amount: number;
  version: number;
}

/** 事务日志（WAL）记录。提交记录是事务的原子边界。 */
export type LogRecord =
  | { kind: 'txn-begin'; txnId: string; eventId: string }
  | { kind: 'order-upsert'; txnId: string; event: OrderEvent }
  | { kind: 'outbox-add'; txnId: string; notification: Notification }
  | { kind: 'txn-commit'; txnId: string }
  | { kind: 'notification-sent'; notificationId: string };

/** 接收方自己的持久日志（与订单库是不同的耐用存储）。 */
export type ReceiverLogRecord = {
  kind: 'notification-received';
  notificationId: string;
  orderId: string;
};
