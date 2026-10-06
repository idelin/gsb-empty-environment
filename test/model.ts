/**
 * 带原始位置标记的参考模型：每个字符携带唯一 id，
 * 连续编辑全程在 raw 坐标系中对 token 序列做 splice + 净化，
 * 光标表示为 token 边界下标。它不经过 display 坐标系，
 * 因此可以与库的实现（display -> raw -> sanitize -> display）
 * 互相核对：字符序列（哪些 id 存活）与光标落点必须一致。
 */

export interface MarkedToken {
  id: number;
  ch: string;
}

export class MarkedModel {
  tokens: MarkedToken[];
  /** token 边界下标，0..tokens.length */
  caret: number;
  private nextId: number;

  private constructor(tokens: MarkedToken[], caret: number, nextId: number) {
    this.tokens = tokens;
    this.caret = caret;
    this.nextId = nextId;
  }

  static fromRaw(raw: string, caret: number = raw.length): MarkedModel {
    const tokens = [...raw].map((ch, i) => ({ id: i + 1, ch }));
    return new MarkedModel(tokens, caret, raw.length + 1);
  }

  get raw(): string {
    return this.tokens.map((t) => t.ch).join("");
  }

  /** 光标右侧 token 的 id（光标在末尾时为 null），用于核对光标落点。 */
  get tokenIdAfterCaret(): number | null {
    return this.caret < this.tokens.length ? this.tokens[this.caret].id : null;
  }

  applyRawEdit(rStart: number, rEnd: number, inserted: string): void {
    const insertedTokens: MarkedToken[] = [...inserted].map((ch) => ({
      id: this.nextId++,
      ch,
    }));
    const merged = [
      ...this.tokens.slice(0, rStart),
      ...insertedTokens,
      ...this.tokens.slice(rEnd),
    ];
    const candidateCaret = rStart + insertedTokens.length;

    const kept: MarkedToken[] = [];
    let seenDot = false;
    let caret = 0;
    for (let i = 0; i < merged.length; i++) {
      const t = merged[i];
      const isDigit = t.ch >= "0" && t.ch <= "9";
      const keep =
        isDigit ||
        (t.ch === "." && !seenDot) ||
        (t.ch === "-" && kept.length === 0);
      if (keep && t.ch === ".") seenDot = true;
      if (keep) {
        kept.push(t);
        if (i < candidateCaret) caret++;
      }
    }
    this.tokens = kept;
    this.caret = caret;
  }
}
