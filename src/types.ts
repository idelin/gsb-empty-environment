export type ObjectRef = { kind: 'object'; name: string; version: number };
export type AliasRef = { kind: 'alias'; name: string };
export type Target = ObjectRef | AliasRef;

export type AliasOp =
  | { type: 'set'; alias: string; target: Target }
  | { type: 'delete'; alias: string };

export type Hop = { alias: string; target: Target };

export type Resolution =
  | { status: 'ok'; path: Hop[]; object: { name: string; version: number } }
  | { status: 'dangling'; path: Hop[]; missing: Target }
  | { status: 'cycle'; path: Hop[]; cycle: string[] }
  | { status: 'max-hops-exceeded'; path: Hop[] }
  | { status: 'missing-alias'; name: string };

export interface Snapshot {
  readonly epoch: number;
  readonly aliases: ReadonlyMap<string, Target>;
  readonly objects: ReadonlyMap<string, ReadonlyMap<number, unknown>>;
}

export function aliasKey(name: string): string {
  return `alias:${name}`;
}

export function objectKey(name: string, version: number): string {
  return `object:${name}@${version}`;
}

export function targetKey(target: Target): string {
  return target.kind === 'alias' ? aliasKey(target.name) : objectKey(target.name, target.version);
}

export function targetEquals(a: Target | undefined, b: Target | undefined): boolean {
  if (a === undefined || b === undefined) return a === b;
  if (a.kind !== b.kind) return false;
  if (a.kind === 'alias' && b.kind === 'alias') return a.name === b.name;
  if (a.kind === 'object' && b.kind === 'object') {
    return a.name === b.name && a.version === b.version;
  }
  return false;
}
