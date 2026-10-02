import { NetworkUnreachableError } from './errors.ts';

/**
 * 可注入故障的网络hub：模拟机房间链路。
 * sever() 模拟光纤挖断，heal() 模拟链路恢复。
 */
export class NetworkHub {
  private readonly severed = new Set<string>();

  private static key(a: string, b: string): string {
    return [a, b].sort().join('<->');
  }

  sever(a: string, b: string): void {
    this.severed.add(NetworkHub.key(a, b));
  }

  heal(a: string, b: string): void {
    this.severed.delete(NetworkHub.key(a, b));
  }

  isConnected(a: string, b: string): boolean {
    return !this.severed.has(NetworkHub.key(a, b));
  }

  /** 通过链路 from->to 发起一次调用；链路断开时抛 NetworkUnreachableError。 */
  send<T>(from: string, to: string, fn: () => T): T {
    if (!this.isConnected(from, to)) {
      throw new NetworkUnreachableError(from, to);
    }
    return fn();
  }
}
