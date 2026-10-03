/**
 * 同步内存集群模拟器：消息进入队列，runRound() 作为一轮通信统一投递，
 * 保证演示与测试完全确定性。
 */
import { Node, type Message } from './node.ts';

export class Cluster {
  readonly nodes: Node[];
  private queue: Message[] = [];
  deliveredCount = 0;

  constructor(size: number, names?: string[]) {
    this.nodes = Array.from(
      { length: size },
      (_, i) => new Node(i, size, names?.[i] ?? `N${i}`),
    );
    for (const node of this.nodes) node.attach(this);
  }

  nameOf(id: number): string {
    return this.nodes[id].name;
  }

  deliver(msg: Message): void {
    this.queue.push(msg);
  }

  get pending(): number {
    return this.queue.length;
  }

  /** 投递当前队列中的全部消息，返回投递条数（一个通信轮次）。 */
  runRound(): number {
    const batch = this.queue.splice(0);
    for (const msg of batch) this.nodes[msg.to].onMessage(msg);
    this.deliveredCount += batch.length;
    return batch.length;
  }

  /** 反复投递直至没有待投递消息（集群静默）。 */
  quiesce(): void {
    while (this.queue.length > 0) this.runRound();
  }
}
