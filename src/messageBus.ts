import type { ClusterMessage } from "./types.ts";

export type MessageHandler = (message: ClusterMessage) => void;

/**
 * 集群广播总线：同步、有序地将控制消息投递给所有订阅者（含发送者自身，
 * 由接收方按 nodeId 自行过滤）。
 */
export class MessageBus {
  private readonly handlers = new Set<MessageHandler>();
  private readonly log: ClusterMessage[] = [];

  subscribe(handler: MessageHandler): () => void {
    this.handlers.add(handler);
    return () => {
      this.handlers.delete(handler);
    };
  }

  broadcast(message: ClusterMessage): void {
    this.log.push(message);
    for (const handler of [...this.handlers]) {
      handler(message);
    }
  }

  history(): readonly ClusterMessage[] {
    return this.log;
  }
}
