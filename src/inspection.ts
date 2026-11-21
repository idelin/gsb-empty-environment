import type { SinkName } from "./sinks.ts";
import { SinkHost, TaintViolation } from "./sinks.ts";
import { TaintHost } from "./host.ts";
import type { ChainStep, OriginFact, Val } from "./tval.ts";
import { TVal } from "./tval.ts";

export interface TaintReport {
  tainted: boolean;
  value: unknown;
  facts: number[];
  chain: ChainStep[];
  origins: OriginFact[];
}

/**
 * 脚本检查服务对外门面：
 * - report：只检查不抛异常，返回污点判定与来源链；
 * - chainString：返回可读来源链；
 * - guard*：把值送入危险宿主，污点即抛 TaintViolation（含来源链）。
 */
export class InspectionService {
  readonly host: TaintHost;
  private readonly sinks: SinkHost;

  constructor(host: TaintHost = new TaintHost()) {
    this.host = host;
    this.sinks = new SinkHost(host);
  }

  report(value: Val): TaintReport {
    const tval = this.host.box(value);
    return {
      tainted: tval.isTainted,
      value: tval.raw,
      facts: [...tval.factIds],
      chain: tval.chain(),
      origins: tval.roots(),
    };
  }

  isTainted(value: Val): boolean {
    return this.host.box(value).isTainted;
  }

  chainString(value: Val): string {
    return this.host.box(value).chainString();
  }

  guard(sink: SinkName, value: Val): unknown {
    switch (sink) {
      case "system":
        return this.sinks.system(value);
      case "exec":
        return this.sinks.exec(value);
      case "backtick":
        return this.sinks.backtick(value);
      case "open-write":
        return this.sinks.open(value);
    }
  }
}

export { TaintHost, TaintViolation, SinkHost };
export { TVal, TaintRegistry } from "./tval.ts";
export type {
  Literal,
  Val,
  ChainStep,
  OriginFact,
  TaintFact,
} from "./tval.ts";
export { TArray, THash } from "./collections.ts";
export { anchoringVerdict } from "./host.ts";
export type { CaptureResult, CaptureGroup } from "./host.ts";
export type { SinkName, SinkResult, OpenResult } from "./sinks.ts";
export { detectMode } from "./sinks.ts";
