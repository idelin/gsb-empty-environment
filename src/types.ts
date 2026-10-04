export interface MessageId {
  node: string;
  epoch: number;
  counter: number;
}

export type PrefixVector = Record<string, number>;

export interface Message {
  id: MessageId;
  deps: PrefixVector;
  payload: unknown;
}

export function streamKey(node: string, epoch: number): string {
  return `${node}@${epoch}`;
}

export function idKey(id: MessageId): string {
  return `${id.node}@${id.epoch}#${id.counter}`;
}

export function canonicalize(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value) ?? "undefined";
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalize).join(",")}]`;
  }
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, v]) => v !== undefined)
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([k, v]) => `${JSON.stringify(k)}:${canonicalize(v)}`);
  return `{${entries.join(",")}}`;
}

export function contentHash(message: Message): string {
  return canonicalize({ deps: message.deps, payload: message.payload });
}
