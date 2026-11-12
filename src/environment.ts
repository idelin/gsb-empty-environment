import type { EnvironmentSnapshot } from './types.ts';

interface TabState {
  documentGeneration: number;
  closed: boolean;
}

/**
 * 模拟浏览器宿主的标签页集合。
 * - 导航（同标签页重新加载文档）只递增该标签页的文档代。
 * - 焦点切换全局单调递增焦点代，并重新绑定聚焦标签页。
 */
export class BrowserEnvironment {
  private readonly tabs = new Map<string, TabState>();
  private focusGeneration = 0;
  private focusedTabId: string | null = null;

  openTab(tabId: string, options: { focused?: boolean } = {}): void {
    if (this.tabs.has(tabId)) {
      throw new Error(`tab ${tabId} already exists`);
    }
    this.tabs.set(tabId, {
      documentGeneration: 1,
      closed: false,
    });
    if (options.focused ?? this.focusedTabId === null) {
      this.focusTab(tabId);
    }
  }

  /** 页面导航：文档代递增；焦点代不变。 */
  navigate(tabId: string): number {
    const tab = this.requireTab(tabId);
    tab.documentGeneration += 1;
    return tab.documentGeneration;
  }

  /** 焦点切换（或同标签页重新聚焦）：全局焦点代递增。 */
  focusTab(tabId: string): number {
    const tab = this.requireTab(tabId);
    this.focusGeneration += 1;
    this.focusedTabId = tabId;
    return this.focusGeneration;
  }

  closeTab(tabId: string): void {
    const tab = this.requireTab(tabId);
    tab.closed = true;
    if (this.focusedTabId === tabId) {
      this.focusedTabId = null;
      this.focusGeneration += 1;
    }
  }

  snapshot(tabId: string): EnvironmentSnapshot {
    const tab = this.requireTab(tabId);
    return {
      tabId,
      documentGeneration: tab.documentGeneration,
      focusGeneration: this.focusGeneration,
    };
  }

  getCurrentSnapshot(tabId: string): EnvironmentSnapshot | null {
    const tab = this.tabs.get(tabId);
    if (!tab || tab.closed) {
      return null;
    }
    return this.snapshot(tabId);
  }

  isFocused(tabId: string): boolean {
    return this.focusedTabId === tabId;
  }

  getFocusedTabId(): string | null {
    return this.focusedTabId;
  }

  getFocusGeneration(): number {
    return this.focusGeneration;
  }

  hasTab(tabId: string): boolean {
    const tab = this.tabs.get(tabId);
    return tab !== undefined && !tab.closed;
  }

  private requireTab(tabId: string): TabState {
    const tab = this.tabs.get(tabId);
    if (!tab) {
      throw new Error(`unknown tab ${tabId}`);
    }
    if (tab.closed) {
      throw new Error(`tab ${tabId} is closed`);
    }
    return tab;
  }
}
