import type {
  AttrKey,
  AttributeSource,
  FetchOutcome,
  FetchRequest,
  Version,
} from '../src/types.ts';

export type Handler = (
  keys: readonly AttrKey[],
  callIndex: number,
  req: FetchRequest,
) => FetchOutcome | Promise<FetchOutcome>;

export interface FetchCall {
  readonly keys: AttrKey[];
  readonly permission: string;
  readonly subjectId: string;
}

export class ScriptedSource implements AttributeSource {
  readonly calls: FetchCall[] = [];
  readonly name: string;
  readonly attrs: ReadonlySet<AttrKey>;
  readonly batchCost: number;
  readonly attrCost: number;
  private readonly handler: Handler;

  constructor(
    name: string,
    attrs: ReadonlySet<AttrKey>,
    batchCost: number,
    attrCost: number,
    handler: Handler,
  ) {
    this.name = name;
    this.attrs = attrs;
    this.batchCost = batchCost;
    this.attrCost = attrCost;
    this.handler = handler;
  }

  fetchBatch(req: FetchRequest): Promise<FetchOutcome> {
    const idx = this.calls.length;
    this.calls.push({
      keys: [...req.keys],
      permission: req.permission,
      subjectId: req.subject.id,
    });
    return Promise.resolve(this.handler(req.keys, idx, req));
  }

  totalCost(): number {
    return this.calls.reduce((sum, c) => sum + this.batchCost + c.keys.length * this.attrCost, 0);
  }
}

export function valuesOutcome(
  version: Version,
  entries: Record<string, unknown>,
  failures?: Record<string, Error>,
): FetchOutcome {
  return {
    version,
    values: new Map(Object.entries(entries)),
    failures: failures ? new Map(Object.entries(failures)) : undefined,
  };
}

export async function waitFor(cond: () => boolean, timeoutMs = 2000): Promise<void> {
  const start = Date.now();
  while (!cond()) {
    if (Date.now() - start > timeoutMs) {
      throw new Error('waitFor timed out');
    }
    await new Promise((r) => setTimeout(r, 5));
  }
}
