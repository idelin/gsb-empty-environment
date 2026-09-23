export type ComputeFn = (inputs: unknown[], prev: unknown) => unknown;

export interface ComputationDefinition {
  id: string;
  version: number;
  compute: ComputeFn;
}

/**
 * Registry of known computation definitions. Functions live only here and are
 * never serialized; persisted graphs refer to definitions by (id, version).
 */
export class DefinitionRegistry {
  private readonly defs = new Map<string, ComputationDefinition>();

  register(def: ComputationDefinition): void {
    this.defs.set(`${def.id}@${def.version}`, def);
  }

  hasId(id: string): boolean {
    for (const key of this.defs.keys()) {
      if (key.startsWith(`${id}@`)) return true;
    }
    return false;
  }

  resolve(id: string, version: number): ComputationDefinition | undefined {
    return this.defs.get(`${id}@${version}`);
  }
}
