/** Lamport 逻辑时钟：发送前自增，接收时取 max(local, remote) + 1。 */
export class LamportClock {
  private time = 0;

  /** 本地事件 / 发送消息时自增。 */
  tick(): number {
    return ++this.time;
  }

  /** 收到携带远程时钟的消息时校正（时钟校正的核心规则）。 */
  witness(remote: number): number {
    this.time = Math.max(this.time, remote) + 1;
    return this.time;
  }

  read(): number {
    return this.time;
  }
}
