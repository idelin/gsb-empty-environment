import { Duplex } from "node:stream";

/**
 * 一对背靠背的内存 Duplex：写入一端的数据可从另一端读出。
 * 沙箱禁止 TCP 监听时，用它替代真实 socket 驱动完整的
 * HTTP Upgrade + RFC 6455 分帧代码路径。
 */
export function duplexPair(): [Duplex, Duplex] {
  const endpoints: Array<{ peer: Duplex | null }> = [{ peer: null }, { peer: null }];

  const make = (id: number): Duplex =>
    new Duplex({
      read() {
        // push 模式：数据在 write 时直接投递，这里无需拉取。
      },
      write(chunk: Buffer, _encoding: BufferEncoding, callback: (error?: Error | null) => void) {
        endpoints[id]!.peer?.push(chunk);
        callback();
      },
      final(callback: (error?: Error | null) => void) {
        endpoints[id]!.peer?.push(null);
        callback();
      },
      destroy(error, callback) {
        const peer = endpoints[id]!.peer;
        if (peer && !peer.destroyed) peer.destroy(error ?? undefined);
        callback(error);
      },
    });

  const first = make(0);
  const second = make(1);
  endpoints[0]!.peer = second;
  endpoints[1]!.peer = first;
  return [first, second];
}
