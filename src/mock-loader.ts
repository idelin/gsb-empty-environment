import type { Loader, LoaderResponse } from "./loader.ts";

export type MockOutcome =
  | { kind: "response"; response: LoaderResponse }
  | { kind: "error"; message: string };

/**
 * 可脚本化的模拟加载器：按 URL 精确匹配预设结果，记录全部尝试。
 * 支持标记缓存命中（fromCache），用于验证缓存不改变安全要求。
 */
export class MockLoader {
  readonly attempts: string[] = [];
  private readonly script = new Map<string, MockOutcome[]>();

  respond(url: string, response: LoaderResponse): this {
    return this.push(url, { kind: "response", response });
  }

  fail(url: string, message: string): this {
    return this.push(url, { kind: "error", message });
  }

  private push(url: string, outcome: MockOutcome): this {
    const list = this.script.get(url) ?? [];
    list.push(outcome);
    this.script.set(url, list);
    return this;
  }

  asLoader(): Loader {
    return async (url: string) => {
      this.attempts.push(url);
      const list = this.script.get(url);
      if (!list || list.length === 0) {
        throw new Error(`mock-loader: no scripted outcome for ${url}`);
      }
      const outcome = list.length > 1 ? list.shift()! : list[0];
      if (outcome.kind === "error") {
        throw new Error(outcome.message);
      }
      return outcome.response;
    };
  }
}
