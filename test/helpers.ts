import { FixedOrderBDD } from '../src/bdd.ts';
import { NonceAuthority, sha256, type SourceExpr } from '../src/index.ts';

export const SWITCHES = ['a', 'b', 'c'] as const;

export function newBdd(): FixedOrderBDD {
  return new FixedOrderBDD([...SWITCHES]);
}

export function source(host: string, pathPrefix = '/js/'): SourceExpr {
  return { scheme: 'https', host, pathPrefix };
}

export function script(id: string, text: string, inheritableSources?: SourceExpr[]) {
  return {
    id,
    bytes: new TextEncoder().encode(text),
    inheritableSources,
  };
}

export function commitmentOf(text: string): string {
  return sha256(new TextEncoder().encode(text));
}

export function authority(responseId: string): NonceAuthority {
  return new NonceAuthority(responseId);
}
