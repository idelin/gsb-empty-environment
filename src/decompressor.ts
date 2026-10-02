import { ByteReader } from './codec.ts';
import { MAGIC } from './compressor.ts';
import type { TrajectoryPoint } from './types.ts';

const FLAG_HEADING_LITERAL = 1 << 0;
const FLAG_ALT_LITERAL = 1 << 1;
const FLAG_SPEED_LITERAL = 1 << 2;
const FLAG_T_LITERAL = 1 << 3;

const LATLON_SCALE = 1e7;
const HEADING_SCALE = 100;
const ALT_SCALE = 100;
const SPEED_SCALE = 1000;

/**
 * 解码压缩数据包。
 * 航向角/速度/高程/时间戳经整数网格状态递推还原，与原始值逐位一致；
 * 经纬度网格精度 1e-7°（≈1.1cm）。
 */
export function decodeTrajectory(data: Uint8Array): TrajectoryPoint[] {
  const r = new ByteReader(data);
  const magic =
    ((r.readByte() << 24) | (r.readByte() << 16) | (r.readByte() << 8) | r.readByte()) >>>
    0;
  if (magic !== MAGIC) throw new Error('bad magic: not a TRJ1 packet');
  const count = r.readUvarint();
  const points: TrajectoryPoint[] = [];
  let prevT = 0;
  let latInt = 0;
  let lonInt = 0;
  let altInt = 0;
  let speedInt = 0;
  let headingInt = 0;
  for (let i = 0; i < count; i++) {
    if (i === 0) {
      const p: TrajectoryPoint = {
        lat: r.readF64(),
        lon: r.readF64(),
        alt: r.readF64(),
        speed: r.readF64(),
        heading: r.readF64(),
        t: r.readF64(),
      };
      latInt = Math.round(p.lat * LATLON_SCALE);
      lonInt = Math.round(p.lon * LATLON_SCALE);
      altInt = Math.round(p.alt * ALT_SCALE);
      speedInt = Math.round(p.speed * SPEED_SCALE);
      headingInt = Math.round(p.heading * HEADING_SCALE);
      prevT = p.t;
      points.push(p);
      continue;
    }
    const flags = r.readByte();
    latInt += r.readSvarint();
    lonInt += r.readSvarint();
    const t = flags & FLAG_T_LITERAL ? r.readF64() : prevT + r.readSvarint();
    const alt =
      flags & FLAG_ALT_LITERAL
        ? r.readF64()
        : (altInt += r.readSvarint()) / ALT_SCALE;
    const speed =
      flags & FLAG_SPEED_LITERAL
        ? r.readF64()
        : (speedInt += r.readSvarint()) / SPEED_SCALE;
    const heading =
      flags & FLAG_HEADING_LITERAL
        ? r.readF64()
        : (headingInt += r.readSvarint()) / HEADING_SCALE;
    // 字面量回退后同步整数状态，与编码器保持一致
    altInt = Math.round(alt * ALT_SCALE);
    speedInt = Math.round(speed * SPEED_SCALE);
    headingInt = Math.round(heading * HEADING_SCALE);
    prevT = t;
    points.push({ lat: latInt / LATLON_SCALE, lon: lonInt / LATLON_SCALE, alt, speed, heading, t });
  }
  return points;
}
