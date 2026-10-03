/**
 * 帧格式（轻量级滑动窗口协议，类 TCP 报文头）：
 *
 *   0        1        5        9        13       15
 *   +--------+--------+--------+--------+--------+-----------------
 *   | flags  |  seq   |  ack   | window | paylen | payload ...
 *   +--------+--------+--------+--------+--------+-----------------
 *
 *   flags  u8   : ACK=0x01, FIN=0x02, PROBE=0x04
 *   seq    u32BE: 本帧 payload 首字节的序号（字节流序号空间）
 *   ack    u32BE: 累积确认号 = 接收端下一个期望字节（流量控制信标）
 *   window u32BE: 接收端当前接收窗口大小（字节），每个 ACK 都携带
 *   paylen u16BE: payload 长度
 */

export const FLAG_ACK = 0x01;
export const FLAG_FIN = 0x02;
export const FLAG_PROBE = 0x04;

export const HEADER_LENGTH = 15;
export const MAX_PAYLOAD = 0xffff;

export interface Frame {
  flags: number;
  seq: number;
  ack: number;
  window: number;
  payload: Buffer;
}

export function encodeFrame(frame: Frame): Buffer {
  if (frame.payload.length > MAX_PAYLOAD) {
    throw new RangeError(`payload too large: ${frame.payload.length}`);
  }
  const buf = Buffer.alloc(HEADER_LENGTH + frame.payload.length);
  buf.writeUInt8(frame.flags & 0xff, 0);
  buf.writeUInt32BE(frame.seq >>> 0, 1);
  buf.writeUInt32BE(frame.ack >>> 0, 5);
  buf.writeUInt32BE(frame.window >>> 0, 9);
  buf.writeUInt16BE(frame.payload.length, 13);
  frame.payload.copy(buf, HEADER_LENGTH);
  return buf;
}

export function decodeFrame(buf: Buffer): Frame {
  if (buf.length < HEADER_LENGTH) {
    throw new RangeError(`frame too short: ${buf.length}`);
  }
  const flags = buf.readUInt8(0);
  const seq = buf.readUInt32BE(1);
  const ack = buf.readUInt32BE(5);
  const window = buf.readUInt32BE(9);
  const paylen = buf.readUInt16BE(13);
  if (buf.length !== HEADER_LENGTH + paylen) {
    throw new RangeError(
      `frame length mismatch: header says ${paylen}, got ${buf.length - HEADER_LENGTH}`,
    );
  }
  return { flags, seq, ack, window, payload: buf.subarray(HEADER_LENGTH) };
}
