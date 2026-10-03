/**
 * 可故障注入的消息传输层。
 *
 * 所有组件（法官、各机房控制器）间通信都经过它路由：
 *  - register() 把一个命名端点（如 "witness" / "dc1"）挂到网络上；
 *  - call() 按 "src->dst" 有向链路寻址并投递请求；
 *  - cut()/partition() 模拟光纤挖断等机房级网络分区，被切断链路上的调用抛错。
 *
 * 生产部署中它对应跨机房 HTTPS RPC；此处用进程内路由以便确定性测试。
 */
export type Handler = (path: string, method: string, body?: unknown) => Promise<unknown>;

export class Network {
  private readonly endpoints = new Map<string, Handler>();
  private readonly cutLinks = new Set<string>();

  register(name: string, handler: Handler): void {
    this.endpoints.set(name, handler);
  }

  unregister(name: string): void {
    this.endpoints.delete(name);
  }

  cut(link: string): void {
    this.cutLinks.add(link);
  }

  heal(link: string): void {
    this.cutLinks.delete(link);
  }

  isCut(link: string): boolean {
    return this.cutLinks.has(link);
  }

  /** 切断 a 与 b 之间的双向链路（模拟光纤挖断）。 */
  partition(a: string, b: string): void {
    this.cut(`${a}->${b}`);
    this.cut(`${b}->${a}`);
  }

  healPartition(a: string, b: string): void {
    this.heal(`${a}->${b}`);
    this.heal(`${b}->${a}`);
  }

  /** 从 src 端点向 dst 端点发起一次调用；链路被切断或对端不存在时抛错。 */
  async call<T>(src: string, dst: string, path: string, method = 'GET', body?: unknown): Promise<T> {
    if (this.cutLinks.has(`${src}->${dst}`)) {
      throw new Error(`network partition: link "${src}->${dst}" is down`);
    }
    const handler = this.endpoints.get(dst);
    if (!handler) {
      throw new Error(`endpoint "${dst}" is unreachable`);
    }
    return (await handler(path, method, body)) as T;
  }
}
