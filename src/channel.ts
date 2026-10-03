/**
 * 抗丢包可靠传输通道。
 * 消息切分为带序号 + CRC32 的帧; LossyLink 按注入器随机丢帧;
 * 接收方报告缺失序号, 发送方重传, 直到完整重组并校验通过。
 */

import { crc32 } from './crc32.ts';
import { InjectedFaultError } from './errors.ts';
import { FaultInjector } from './faults.ts';

const FRAME_HEADER = 12; // seq u32 | total u32 | len u32
const FRAME_CRC = 4;

export function encodeFrame(seq: number, total: number, payload: Uint8Array): Uint8Array {
  const out = new Uint8Array(FRAME_HEADER + payload.length + FRAME_CRC);
  const view = new DataView(out.buffer);
  view.setUint32(0, seq, true);
  view.setUint32(4, total, true);
  view.setUint32(8, payload.length, true);
  out.set(payload, FRAME_HEADER);
  view.setUint32(FRAME_HEADER + payload.length, crc32(out.subarray(0, FRAME_HEADER + payload.length)), true);
  return out;
}

export function decodeFrame(frame: Uint8Array): { seq: number; total: number; payload: Uint8Array } {
  if (frame.length < FRAME_HEADER + FRAME_CRC) {
    throw new InjectedFaultError('packet-loss', '帧长度不足, 视为损坏帧');
  }
  const view = new DataView(frame.buffer, frame.byteOffset, frame.length);
  const stored = view.getUint32(frame.length - FRAME_CRC, true);
  if (crc32(frame.subarray(0, frame.length - FRAME_CRC)) !== stored) {
    throw new InjectedFaultError('packet-loss', '帧 CRC 校验失败, 视为损坏帧');
  }
  return {
    seq: view.getUint32(0, true),
    total: view.getUint32(4, true),
    payload: frame.subarray(FRAME_HEADER, frame.length - FRAME_CRC),
  };
}

/** 有损链路: 每次发送可能丢帧。 */
export class LossyLink {
  sent = 0;
  lost = 0;
  private readonly injector: FaultInjector;
  constructor(injector: FaultInjector) {
    this.injector = injector;
  }

  transmit(frame: Uint8Array): Uint8Array | null {
    this.sent++;
    if (this.injector.shouldFire('packet-loss')) {
      this.lost++;
      return null;
    }
    return frame;
  }
}

export interface TransferStats {
  framesTotal: number;
  framesSent: number;
  framesLost: number;
  retransmissions: number;
  rounds: number;
}

/**
 * 可靠传输: 切帧 -> 发送 -> 收集 ACK 缺口 -> 重传, 直至重组完成。
 * maxRounds 兜底防死循环。
 */
export function sendReliable(
  link: LossyLink,
  message: Uint8Array,
  framePayloadSize = 64,
  maxRounds = 1000,
): { received: Uint8Array; stats: TransferStats } {
  const total = Math.max(1, Math.ceil(message.length / framePayloadSize));
  const frames: Uint8Array[] = [];
  for (let i = 0; i < total; i++) {
    frames.push(
      encodeFrame(i, total, message.subarray(i * framePayloadSize, (i + 1) * framePayloadSize)),
    );
  }

  const receivedFrames = new Map<number, Uint8Array>();
  let rounds = 0;
  let retransmissions = 0;

  while (receivedFrames.size < total) {
    if (++rounds > maxRounds) {
      throw new Error(`传输超过 ${maxRounds} 轮仍未完成, 判定链路不可用`);
    }
    const missing: number[] = [];
    for (let i = 0; i < total; i++) {
      if (!receivedFrames.has(i)) missing.push(i);
    }
    if (rounds > 1) retransmissions += missing.length;
    for (const seq of missing) {
      const frame = link.transmit(frames[seq]);
      if (frame === null) continue; // 丢包
      try {
        const decoded = decodeFrame(frame);
        receivedFrames.set(decoded.seq, decoded.payload);
      } catch {
        // 损坏帧等同丢包, 等待重传
      }
    }
  }

  const out = new Uint8Array(message.length);
  let off = 0;
  for (let i = 0; i < total; i++) {
    out.set(receivedFrames.get(i)!, off);
    off += receivedFrames.get(i)!.length;
  }
  return {
    received: out,
    stats: {
      framesTotal: total,
      framesSent: link.sent,
      framesLost: link.lost,
      retransmissions,
      rounds,
    },
  };
}
