/**
 * CABAC 内核：上下文自适应二进制算术编码（仿 H.264/AVC 的 M-coder）。
 *
 * - 每个语法元素的每个 bin 绑定一个独立的 ContextModel（64 状态 + MPS 方向）；
 * - 每编码一个 bin，按命中情况沿 TRANS_IDX_MPS / TRANS_IDX_LPS 迁移状态编号，
 *   实现对局部统计特性的在线跟踪（"越命中越自信"）；
 * - adaptive = false 时状态冻结，退化为无上下文的静态算术编码（对照基线）。
 */
import { RANGE_TAB_LPS, TRANS_IDX_LPS, TRANS_IDX_MPS } from './tables.ts';
import { BitWriter, BitReader } from './bitstream.ts';

export interface ContextModel {
  /** 概率状态编号 0..63 */
  state: number;
  /** 当前最可能符号 */
  mps: 0 | 1;
}

export function createContext(): ContextModel {
  return { state: 0, mps: 0 }; // 无先验：p = 0.5
}

export function createContexts(count: number): ContextModel[] {
  return Array.from({ length: count }, createContext);
}

const QUARTER = 256; // range 下界
const HALF = 512;

export class CabacEncoder {
  private low = 0;
  private range = HALF - 2; // 510
  private outstanding = 0;
  private firstBit = true; // 首个输出比特为初始窗口的冗余前导位，按规范丢弃
  private readonly writer: BitWriter;
  private readonly adaptive: boolean;

  constructor(writer: BitWriter, adaptive = true) {
    this.writer = writer;
    this.adaptive = adaptive;
  }

  encodeBin(bin: 0 | 1, ctx: ContextModel): void {
    const rLPS = RANGE_TAB_LPS[ctx.state][(this.range >> 6) & 3];
    this.range -= rLPS;
    if (bin !== ctx.mps) {
      this.low += this.range;
      this.range = rLPS;
      if (this.adaptive) {
        if (ctx.state === 0) ctx.mps = (1 - ctx.mps) as 0 | 1;
        ctx.state = TRANS_IDX_LPS[ctx.state];
      }
    } else if (this.adaptive) {
      ctx.state = TRANS_IDX_MPS[ctx.state];
    }
    this.renormalize();
  }

  private renormalize(): void {
    while (this.range < QUARTER) {
      if (this.low >= HALF) {
        this.putBit(1);
        this.low -= HALF;
      } else if (this.low < QUARTER) {
        this.putBit(0);
      } else {
        this.outstanding++; // 进位待决，延迟输出
        this.low -= QUARTER;
      }
      this.low <<= 1;
      this.range <<= 1;
    }
  }

  private putBit(bit: 0 | 1): void {
    if (this.firstBit) {
      this.firstBit = false;
    } else {
      this.writer.writeBit(bit);
    }
    const follow = (1 - bit) as 0 | 1;
    while (this.outstanding > 0) {
      this.writer.writeBit(follow);
      this.outstanding--;
    }
  }

  /**
   * 冲刷编码器，返回压缩后的字节流。
   * 最终区间 [low, low+range) 内必存在 256 的倍数 m（range ≥ 256 保证），
   * 输出 m 的高两位（首位经 outstanding 进位解析）；解码端零填充后
   * 码字恰为 m，保证落在最终区间内，且严格避开区间边界。
   */
  finish(): Uint8Array {
    const m = this.low % QUARTER === 0 ? this.low : ((this.low >> 8) + 1) << 8;
    this.putBit(((m >> 9) & 1) as 0 | 1);
    this.writer.writeBit(((m >> 8) & 1) as 0 | 1);
    return this.writer.finish();
  }
}

export class CabacDecoder {
  private range = HALF - 2;
  private value = 0;
  private readonly reader: BitReader;
  private readonly adaptive: boolean;

  constructor(reader: BitReader, adaptive = true) {
    this.reader = reader;
    this.adaptive = adaptive;
    for (let i = 0; i < 9; i++) {
      this.value = (this.value << 1) | this.reader.readBit();
    }
  }

  decodeBin(ctx: ContextModel): 0 | 1 {
    const rLPS = RANGE_TAB_LPS[ctx.state][(this.range >> 6) & 3];
    this.range -= rLPS;
    let bin: 0 | 1;
    if (this.value >= this.range) {
      bin = (1 - ctx.mps) as 0 | 1; // LPS
      this.value -= this.range;
      this.range = rLPS;
      if (this.adaptive) {
        if (ctx.state === 0) ctx.mps = (1 - ctx.mps) as 0 | 1;
        ctx.state = TRANS_IDX_LPS[ctx.state];
      }
    } else {
      bin = ctx.mps; // MPS
      if (this.adaptive) ctx.state = TRANS_IDX_MPS[ctx.state];
    }
    while (this.range < QUARTER) {
      this.range <<= 1;
      this.value = (this.value << 1) | this.reader.readBit();
    }
    return bin;
  }
}
