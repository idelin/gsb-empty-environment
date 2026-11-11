import { util } from './node-compat.ts';
import { CapabilityLeakError, ProxyRejectedError } from './errors.ts';

export type Capability = object;

export class CapabilityRegistry {
  readonly #handles = new Set<Capability>();

  register(handle: Capability): Capability {
    if (util.types.isProxy(handle)) {
      throw new ProxyRejectedError();
    }
    this.#handles.add(handle);
    return handle;
  }

  unregister(handle: Capability): void {
    this.#handles.delete(handle);
  }

  has(handle: object): boolean {
    return this.#handles.has(handle);
  }

  get size(): number {
    return this.#handles.size;
  }
}

export function assertNotCapability(
  registry: CapabilityRegistry | undefined,
  value: object,
): void {
  if (registry !== undefined && registry.has(value)) {
    throw new CapabilityLeakError();
  }
}
