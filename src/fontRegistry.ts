/**
 * 字体注册表：
 * - 快照不可变，热更新通过单次引用替换完成原子代际切换；
 * - 晚到的字体加载结果必须校验版本，过期加载不得覆盖新版本。
 */
import type { FontFace } from './types.ts';

export class FontSnapshot {
  readonly fonts: ReadonlyMap<string, FontFace>;
  readonly generation: number;

  constructor(fonts: ReadonlyMap<string, FontFace>, generation: number) {
    this.fonts = fonts;
    this.generation = generation;
    Object.freeze(this);
  }

  get(fontId: string): FontFace | undefined {
    return this.fonts.get(fontId);
  }
}

export type FontChangeListener = (snapshot: FontSnapshot, changed: FontFace) => void;

export class FontRegistry {
  private snapshotValue: FontSnapshot;
  private readonly listeners = new Set<FontChangeListener>();

  constructor(initial: readonly FontFace[] = []) {
    this.snapshotValue = new FontSnapshot(new Map(initial.map((f) => [f.id, f])), 0);
  }

  /** 原子读：调用方拿到的快照在热更新前后保持一致。 */
  snapshot(): FontSnapshot {
    return this.snapshotValue;
  }

  onChange(listener: FontChangeListener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /**
   * 热更新：构造新快照后单次赋值切换代际，
   * 读者要么看到完整旧快照，要么看到完整新快照。
   */
  hotReload(face: FontFace): FontSnapshot {
    const prev = this.snapshotValue;
    const existing = prev.fonts.get(face.id);
    if (existing && face.version <= existing.version) {
      throw new Error(`字体 ${face.id} 版本必须递增: 当前 ${existing.version}, 收到 ${face.version}`);
    }
    const fonts = new Map(prev.fonts);
    fonts.set(face.id, face);
    const next = new FontSnapshot(fonts, prev.generation + 1);
    this.snapshotValue = next; // 原子切换
    for (const listener of [...this.listeners]) listener(next, face);
    return next;
  }

  /**
   * 异步加载字体。加载完成时校验版本：
   * 若期间已有更新版本（热更新），晚到的旧结果直接丢弃。
   */
  async loadFont(loader: () => Promise<FontFace>): Promise<{ applied: boolean; face: FontFace }> {
    const face = await loader();
    const current = this.snapshotValue.fonts.get(face.id);
    if (current && face.version <= current.version) {
      return { applied: false, face }; // 晚到的旧版本，拒绝覆盖
    }
    this.hotReload(face);
    return { applied: true, face };
  }
}
