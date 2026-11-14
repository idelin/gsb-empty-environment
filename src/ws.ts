import { randomBytes } from "node:crypto";
import type { Duplex } from "node:stream";

export const OPCODES = {
  text: 0x1,
  binary: 0x2,
  close: 0x8,
  ping: 0x9,
  pong: 0xa,
} as const;

export interface Frame {
  readonly fin: boolean;
  readonly opcode: number;
  readonly payload: Buffer;
}

/** Incremental parser for WebSocket frames arriving on a socket. */
export class FrameParser {
  private buffer = Buffer.alloc(0);

  push(chunk: Buffer): Frame[] {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    const frames: Frame[] = [];
    for (;;) {
      const frame = this.tryParse();
      if (!frame) break;
      frames.push(frame);
    }
    return frames;
  }

  private tryParse(): Frame | null {
    const buf = this.buffer;
    if (buf.length < 2) return null;
    const fin = (buf[0] & 0x80) !== 0;
    const opcode = buf[0] & 0x0f;
    const masked = (buf[1] & 0x80) !== 0;
    let length = buf[1] & 0x7f;
    let offset = 2;
    if (length === 126) {
      if (buf.length < offset + 2) return null;
      length = buf.readUInt16BE(offset);
      offset += 2;
    } else if (length === 127) {
      if (buf.length < offset + 8) return null;
      const big = buf.readBigUInt64BE(offset);
      if (big > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error("frame too large");
      length = Number(big);
      offset += 8;
    }
    let mask: Buffer | null = null;
    if (masked) {
      if (buf.length < offset + 4) return null;
      mask = buf.subarray(offset, offset + 4);
      offset += 4;
    }
    if (buf.length < offset + length) return null;
    let payload = Buffer.from(buf.subarray(offset, offset + length));
    if (mask) {
      for (let i = 0; i < payload.length; i++) payload[i] ^= mask[i % 4];
    }
    this.buffer = buf.subarray(offset + length);
    return { fin, opcode, payload };
  }
}

export function encodeFrame(opcode: number, payload: Buffer, mask: boolean): Buffer {
  const length = payload.length;
  let header: Buffer;
  if (length < 126) {
    header = Buffer.alloc(2);
    header[1] = length;
  } else if (length < 65536) {
    header = Buffer.alloc(4);
    header[1] = 126;
    header.writeUInt16BE(length, 2);
  } else {
    header = Buffer.alloc(10);
    header[1] = 127;
    header.writeBigUInt64BE(BigInt(length), 2);
  }
  header[0] = 0x80 | opcode;
  if (!mask) return Buffer.concat([header, payload]);
  const maskKey = randomBytes(4);
  header[1] |= 0x80;
  const masked = Buffer.from(payload);
  for (let i = 0; i < masked.length; i++) masked[i] ^= maskKey[i % 4];
  return Buffer.concat([header, maskKey, masked]);
}

export function sendText(socket: Duplex, data: string, mask = false): void {
  socket.write(encodeFrame(OPCODES.text, Buffer.from(data, "utf8"), mask));
}

export function sendClose(socket: Duplex, code: number, reason: string, mask = false): void {
  const reasonBuf = Buffer.from(reason, "utf8");
  const payload = Buffer.alloc(2 + reasonBuf.length);
  payload.writeUInt16BE(code, 0);
  reasonBuf.copy(payload, 2);
  socket.write(encodeFrame(OPCODES.close, payload, mask));
}
