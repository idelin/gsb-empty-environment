import { planRequest, type PlanInput } from "./planner.ts";
import type { RequestPlan } from "./types.ts";

export interface LoaderResponse {
  status: number;
  /** 3xx 重定向目标（可以是相对 URL）。 */
  redirectTo?: string;
  /** 命中缓存时由宿主标记；不影响安全要求。 */
  fromCache?: boolean;
  body?: string;
}

/** 浏览器宿主提供的实际加载能力（fetch 的抽象）。 */
export type Loader = (url: string) => Promise<LoaderResponse>;

export interface LoadResult {
  ok: boolean;
  /** 每次发起网络请求前形成的规划链（含每次重定向后的重新规划）。 */
  plans: RequestPlan[];
  /** 实际交给 loader 的 URL 序列。 */
  attempts: string[];
  response?: LoaderResponse;
  error?: string;
}

export interface LoadOptions extends Omit<PlanInput, "url" | "baseUrl"> {
  url: string;
  loader: Loader;
  maxRedirects?: number;
}

/**
 * 按规划执行加载：
 * - 每个重定向跳点都重新走 planRequest，升级要求沿重定向链持续生效；
 * - 升级后的请求失败时直接失败，绝不悄悄回落明文；
 * - 阻断时不发起任何网络请求。
 */
export async function loadWithPlan(options: LoadOptions): Promise<LoadResult> {
  const { loader, maxRedirects = 5, ...planBase } = options;
  const plans: RequestPlan[] = [];
  const attempts: string[] = [];

  let currentUrl = options.url;
  for (let hop = 0; hop <= maxRedirects; hop++) {
    const plan = planRequest({ ...planBase, url: currentUrl });
    plans.push(plan);
    if (plan.action === "block") {
      return { ok: false, plans, attempts, error: `blocked: ${plan.reason}` };
    }

    attempts.push(plan.url);
    let response: LoaderResponse;
    try {
      response = await loader(plan.url);
    } catch (err) {
      // 失败即失败：不重写成 originalUrl 重试，不明文回落。
      return { ok: false, plans, attempts, error: `load-failed: ${(err as Error).message}` };
    }

    if (response.redirectTo !== undefined) {
      if (hop === maxRedirects) {
        return { ok: false, plans, attempts, error: "too-many-redirects" };
      }
      currentUrl = new URL(response.redirectTo, plan.url).toString();
      continue;
    }
    return { ok: true, plans, attempts, response };
  }
  return { ok: false, plans, attempts, error: "too-many-redirects" };
}
