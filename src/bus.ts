import type { EnvelopedMessage, Message, MessageHandler } from "./messages.ts";

interface NodeChannel {
  handler: MessageHandler;
  connected: boolean;
  paused: boolean;
  queue: EnvelopedMessage[];
  lastSeq: number;
}

/**
 * 消息总线：保留完整消息日志。
 * - 连接且未暂停的节点同步收到消息；
 * - 暂停时消息进入节点队列（可用 deliverNext 乱序投递，模拟消息重排）；
 * - 断线期间的消息在 reconnect 时按序号重放，保证不丢屏障。
 */
export class MessageBus {
  private log: EnvelopedMessage[] = [];
  private channels = new Map<string, NodeChannel>();
  private seq = 0;

  connect(nodeId: string, handler: MessageHandler): void {
    const channel: NodeChannel = {
      handler,
      connected: true,
      paused: false,
      queue: [],
      lastSeq: 0,
    };
    this.channels.set(nodeId, channel);
    // 新加入的节点补发全部历史消息（快照同步），避免错过已发布的屏障。
    for (const msg of this.log) this.deliver(channel, msg);
  }

  disconnect(nodeId: string): void {
    const channel = this.mustGet(nodeId);
    channel.connected = false;
    channel.queue = [];
  }

  reconnect(nodeId: string): void {
    const channel = this.mustGet(nodeId);
    channel.connected = true;
    for (const msg of this.log) {
      if (msg.seq > channel.lastSeq) this.deliver(channel, msg);
    }
  }

  pause(nodeId: string): void {
    this.mustGet(nodeId).paused = true;
  }

  resume(nodeId: string): void {
    const channel = this.mustGet(nodeId);
    channel.paused = false;
    while (channel.queue.length > 0) {
      this.deliver(channel, channel.queue.shift() as EnvelopedMessage);
    }
  }

  publish(message: Message): void {
    const enveloped: EnvelopedMessage = { ...message, seq: ++this.seq };
    this.log.push(enveloped);
    for (const channel of this.channels.values()) {
      if (!channel.connected) continue;
      if (channel.paused) channel.queue.push(enveloped);
      else this.deliver(channel, enveloped);
    }
  }

  /** 测试钩子：从节点队列中挑出第一条匹配的消息先行投递（模拟乱序到达）。 */
  deliverNext(
    nodeId: string,
    predicate: (msg: EnvelopedMessage) => boolean = () => true,
  ): boolean {
    const channel = this.mustGet(nodeId);
    const index = channel.queue.findIndex(predicate);
    if (index < 0) return false;
    const [msg] = channel.queue.splice(index, 1);
    this.deliver(channel, msg);
    return true;
  }

  pending(nodeId: string): readonly EnvelopedMessage[] {
    return this.mustGet(nodeId).queue;
  }

  private deliver(channel: NodeChannel, msg: EnvelopedMessage): void {
    if (msg.seq > channel.lastSeq) channel.lastSeq = msg.seq;
    channel.handler(msg);
  }

  private mustGet(nodeId: string): NodeChannel {
    const channel = this.channels.get(nodeId);
    if (!channel) throw new Error(`unknown node: ${nodeId}`);
    return channel;
  }
}
