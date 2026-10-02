import type { TrajectoryPoint } from './types.ts';

/**
 * Compact binary packet codec for the retained trajectory points.
 *
 * The codec is lossless by design: every retained field is stored at full
 * precision, so the decoded stream is bit-for-bit identical to the
 * compressor output. Heading angle and turn radius therefore suffer zero
 * distortion after restoration. The size savings come from adaptive
 * sampling (upstream) and compact binary framing (here) — not from
 * lossy quantization.
 *
 * Layout (little-endian):
 *   magic    4 bytes  "TJC1"
 *   count    u32
 *   first point:
 *     t       f64  epoch ms (absolute)
 *     lat     f64  degrees
 *     lon     f64  degrees
 *     ele     f64  meters
 *     speed   f64  m/s
 *     heading f64  degrees
 *   subsequent points:
 *     dt      uvarint  ms delta (exact integer)
 *     lat     f64
 *     lon     f64
 *     ele     f64
 *     speed   f64
 *     heading f64
 */

const MAGIC = 0x31434a54; // "TJC1" little-endian

class ByteWriter {
  private buf: number[] = [];
  private readonly scratch = new DataView(new ArrayBuffer(8));

  u32(v: number): void {
    this.scratch.setUint32(0, v >>> 0, true);
    for (let i = 0; i < 4; i++) this.buf.push(this.scratch.getUint8(i));
  }
  f64(v: number): void {
    this.scratch.setFloat64(0, v, true);
    for (let i = 0; i < 8; i++) this.buf.push(this.scratch.getUint8(i));
  }
  uvarint(v: number): void {
    let n = v;
    while (n >= 0x80) {
      this.buf.push((n & 0x7f) | 0x80);
      n = Math.floor(n / 128);
    }
    this.buf.push(n);
  }
  bytes(): Uint8Array {
    return Uint8Array.from(this.buf);
  }
}

class ByteReader {
  private pos = 0;
  private readonly view: DataView;
  private readonly buf: Uint8Array;
  constructor(buf: Uint8Array) {
    this.buf = buf;
    this.view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
  }
  u32(): number {
    const v = this.view.getUint32(this.pos, true);
    this.pos += 4;
    return v;
  }
  f64(): number {
    const v = this.view.getFloat64(this.pos, true);
    this.pos += 8;
    return v;
  }
  uvarint(): number {
    let result = 0;
    let shift = 1;
    for (;;) {
      const b = this.buf[this.pos++];
      result += (b & 0x7f) * shift;
      if ((b & 0x80) === 0) break;
      shift *= 128;
    }
    return result;
  }
}

/** Encode retained points into a compact lossless packet. */
export function encodePacket(points: TrajectoryPoint[]): Uint8Array {
  const w = new ByteWriter();
  w.u32(MAGIC);
  w.u32(points.length);
  if (points.length === 0) return w.bytes();

  w.f64(points[0].t);
  w.f64(points[0].lat);
  w.f64(points[0].lon);
  w.f64(points[0].ele);
  w.f64(points[0].speed);
  w.f64(points[0].heading);

  for (let i = 1; i < points.length; i++) {
    const p = points[i];
    w.uvarint(Math.round(p.t - points[i - 1].t));
    w.f64(p.lat);
    w.f64(p.lon);
    w.f64(p.ele);
    w.f64(p.speed);
    w.f64(p.heading);
  }
  return w.bytes();
}

/** Decode a packet back into trajectory points (bit-exact). */
export function decodePacket(packet: Uint8Array): TrajectoryPoint[] {
  const r = new ByteReader(packet);
  if (r.u32() !== MAGIC) {
    throw new Error('bad packet magic');
  }
  const count = r.u32();
  if (count === 0) return [];

  const out: TrajectoryPoint[] = [];
  out.push({
    t: r.f64(),
    lat: r.f64(),
    lon: r.f64(),
    ele: r.f64(),
    speed: r.f64(),
    heading: r.f64(),
  });
  for (let i = 1; i < count; i++) {
    const dt = r.uvarint();
    out.push({
      t: out[i - 1].t + dt,
      lat: r.f64(),
      lon: r.f64(),
      ele: r.f64(),
      speed: r.f64(),
      heading: r.f64(),
    });
  }
  return out;
}
