/**
 * Wire format of the lightweight sliding-window flow-control protocol.
 *
 * Every frame carries a fixed 16-byte header. DATA frames are followed by a
 * payload. ACK frames are pure "flow-control beacons": they carry no payload,
 * only the cumulative ACK and the receiver's current window size. Every frame
 * (including DATA and WINDOW_PROBE) piggybacks the sender's own beacon values,
 * which makes the signalling bidirectional.
 *
 *   0                   1                   2                   3
 *   0 1 2 3 4 5 6 7 8 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
 *  +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
 *  |     Type      |     Flags     |        Payload Length         |
 *  +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
 *  |                        Sequence Number                        |
 *  +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
 *  |                     Cumulative ACK Number                     |
 *  +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
 *  |                     Advertised Window Size                    |
 *  +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
 */

export const HEADER_LENGTH = 16;

export const FrameType = {
  /** Carries application payload bytes. */
  DATA: 0x01,
  /** Pure flow-control beacon: cumulative ACK + advertised window. */
  ACK: 0x02,
  /** Zero-window probe: asks the peer to re-beacon its current window. */
  WINDOW_PROBE: 0x03,
} as const;

export type FrameType = (typeof FrameType)[keyof typeof FrameType];

export interface Frame {
  type: FrameType;
  /** Byte offset of the payload in the sender's stream (DATA frames). */
  seq: number;
  /** Cumulative ACK: next byte offset the sender of this frame expects. */
  ack: number;
  /** Receive-window beacon of the sender of this frame, in bytes. */
  window: number;
  payload: Buffer;
}

export const EMPTY_PAYLOAD: Buffer = Buffer.alloc(0);

export function encodeFrame(frame: Frame): Buffer {
  if (frame.payload.length > 0xffff) {
    throw new RangeError(`payload too large for header: ${frame.payload.length}`);
  }
  const buf = Buffer.alloc(HEADER_LENGTH + frame.payload.length);
  buf.writeUInt8(frame.type, 0);
  buf.writeUInt8(0, 1); // flags, reserved
  buf.writeUInt16BE(frame.payload.length, 2);
  buf.writeUInt32BE(frame.seq >>> 0, 4);
  buf.writeUInt32BE(frame.ack >>> 0, 8);
  buf.writeUInt32BE(frame.window >>> 0, 12);
  frame.payload.copy(buf, HEADER_LENGTH);
  return buf;
}

export function decodeFrame(buf: Buffer): Frame {
  if (buf.length < HEADER_LENGTH) {
    throw new RangeError(`frame too short: ${buf.length} < ${HEADER_LENGTH}`);
  }
  const type = buf.readUInt8(0) as FrameType;
  const payloadLength = buf.readUInt16BE(2);
  if (buf.length !== HEADER_LENGTH + payloadLength) {
    throw new RangeError(
      `frame length mismatch: buffer=${buf.length}, header+payload=${HEADER_LENGTH + payloadLength}`,
    );
  }
  return {
    type,
    seq: buf.readUInt32BE(4),
    ack: buf.readUInt32BE(8),
    window: buf.readUInt32BE(12),
    payload: Buffer.from(buf.subarray(HEADER_LENGTH)),
  };
}

export function frameTypeName(type: FrameType): string {
  switch (type) {
    case FrameType.DATA:
      return 'DATA';
    case FrameType.ACK:
      return 'ACK';
    case FrameType.WINDOW_PROBE:
      return 'WINDOW_PROBE';
    default:
      return `UNKNOWN(${type})`;
  }
}
