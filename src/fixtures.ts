/**
 * Deterministic fixtures: the node tree, the glyph metrics, and a manually
 * controlled async loader so tests never depend on timers.
 */

import type { NodeData } from "./model.ts";
import type { GlyphMetrics } from "./metrics.ts";

export function treeFixture(): NodeData {
  return {
    id: "root",
    label: "Files",
    children: [
      {
        id: "src",
        label: "src",
        children: [
          { id: "app", label: "app.ts" },
          { id: "util", label: "util.ts", disabled: true },
          { id: "lib", label: "lib", lazy: true },
        ],
      },
      {
        id: "docs",
        label: "docs",
        children: [
          { id: "guide", label: "guide.md" },
          { id: "api", label: "api.md" },
        ],
      },
      {
        id: "scripts",
        label: "scripts",
        disabled: true,
        children: [
          { id: "build", label: "build.sh" },
          { id: "clean", label: "clean.sh" },
        ],
      },
      { id: "readme", label: "README.md" },
    ],
  };
}

export function metricsFixture(): GlyphMetrics {
  return { rowHeight: 22, charWidth: 7, indentWidth: 16, toggleWidth: 20 };
}

/** Children served lazily by the loader. */
export function lazyChildrenFixture(): Record<string, NodeData[]> {
  return {
    lib: [
      { id: "lib-core", label: "core.ts" },
      { id: "lib-io", label: "io.ts", disabled: true },
      { id: "lib-extra", label: "extra", lazy: true },
    ],
    "lib-extra": [{ id: "lib-extra-a", label: "a.ts" }],
  };
}

interface Deferred {
  resolve: (value: NodeData[]) => void;
  reject: (err: unknown) => void;
}

/** A loader whose promises only settle when the test says so. */
export class DeferredLoader {
  private pending = new Map<string, Deferred[]>();
  readonly calls: string[] = [];
  private readonly children: Record<string, NodeData[]>;

  constructor(children: Record<string, NodeData[]>) {
    this.children = children;
  }

  load = (id: string): Promise<NodeData[]> => {
    this.calls.push(id);
    return new Promise<NodeData[]>((resolve, reject) => {
      const list = this.pending.get(id) ?? [];
      list.push({ resolve, reject });
      this.pending.set(id, list);
    });
  };

  pendingCount(id: string): number {
    return this.pending.get(id)?.length ?? 0;
  }

  /** Resolve the oldest pending load for `id` with the fixture children. */
  resolveNext(id: string): void {
    const list = this.pending.get(id);
    if (!list || list.length === 0) throw new Error(`no pending load for ${id}`);
    list.shift()!.resolve(this.children[id] ?? []);
  }

  rejectNext(id: string, err: unknown = new Error("load failed")): void {
    const list = this.pending.get(id);
    if (!list || list.length === 0) throw new Error(`no pending load for ${id}`);
    list.shift()!.reject(err);
  }
}

/** Flush the microtask queue so resolved loads get applied. */
export async function flush(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
}
