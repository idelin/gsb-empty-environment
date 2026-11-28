import { LabelSelector, ParameterObject, ParamRef } from './types.ts';
import { labelSelectorMatches } from './matching.ts';

export interface ParameterKey {
  namespace: string;
  name: string;
}

export interface ParameterResolution {
  found: boolean;
  objects: Array<{ key: ParameterKey; object: ParameterObject }>;
}

export class ParameterStore {
  private readonly objects = new Map<string, ParameterObject>();

  private static keyOf(namespace: string, name: string): string {
    return `${namespace}/${name}`;
  }

  upsert(object: ParameterObject): void {
    const namespace = object.metadata?.namespace ?? '';
    const name = object.metadata?.name;
    if (!name) throw new Error('parameter object requires metadata.name');
    this.objects.set(ParameterStore.keyOf(namespace, name), object);
  }

  remove(namespace: string, name: string): void {
    this.objects.delete(ParameterStore.keyOf(namespace, name));
  }

  get(namespace: string, name: string): ParameterObject | undefined {
    return this.objects.get(ParameterStore.keyOf(namespace, name));
  }

  list(): ParameterObject[] {
    return [...this.objects.values()];
  }

  /**
   * Resolve a binding's paramRef.
   *
   * - A fixed `name` resolves at most one object (in `namespace` when provided).
   * - A `selector` expands to every matching parameter object in the given
   *     namespace, or cluster-wide when no namespace is given.
   * - Selector matches also honor the policy's paramKind apiVersion/kind when present.
   */
  resolve(
    paramRef: ParamRef | undefined,
    paramKind: { apiVersion?: string; kind?: string } | undefined,
    requestNamespace?: string,
  ): ParameterResolution {
    if (!paramRef) {
      return { found: true, objects: [] };
    }

    const namespace = paramRef.namespace ?? '';

    if (paramRef.name) {
      const object = this.findByName(namespace, paramRef.name, paramKind);
      if (!object) return { found: false, objects: [] };
      return {
        found: true,
        objects: [{ key: { namespace, name: paramRef.name }, object }],
      };
    }

    if (paramRef.selector) {
      const matches = this.findBySelector(
        paramRef.selector,
        namespace,
        paramKind,
        requestNamespace,
      );
      return { found: matches.length > 0, objects: matches };
    }

    // ParamRef present but neither name nor selector: nothing can match.
    return { found: false, objects: [] };
  }

  private findByName(
    namespace: string,
    name: string,
    paramKind: { apiVersion?: string; kind?: string } | undefined,
  ): ParameterObject | undefined {
    if (namespace) {
      const direct = this.get(namespace, name);
      if (direct && this.kindMatches(direct, paramKind)) return direct;
      return undefined;
    }
    for (const object of this.objects.values()) {
      if (object.metadata?.name !== name) continue;
      if (!this.kindMatches(object, paramKind)) continue;
      return object;
    }
    return undefined;
  }

  private findBySelector(
    selector: LabelSelector,
    namespace: string,
    paramKind: { apiVersion?: string; kind?: string } | undefined,
    _requestNamespace?: string,
  ): Array<{ key: ParameterKey; object: ParameterObject }> {
    const out: Array<{ key: ParameterKey; object: ParameterObject }> = [];
    for (const object of this.objects.values()) {
      const objectNamespace = object.metadata?.namespace ?? '';
      if (namespace && objectNamespace !== namespace) continue;
      if (!this.kindMatches(object, paramKind)) continue;
      if (!labelSelectorMatches(selector, object.metadata?.labels)) continue;
      out.push({
        key: { namespace: objectNamespace, name: object.metadata?.name ?? '' },
        object,
      });
    }
    out.sort((a, b) => {
      const ns = a.key.namespace.localeCompare(b.key.namespace);
      return ns !== 0 ? ns : a.key.name.localeCompare(b.key.name);
    });
    return out;
  }

  private kindMatches(
    object: ParameterObject,
    paramKind: { apiVersion?: string; kind?: string } | undefined,
  ): boolean {
    if (!paramKind) return true;
    if (paramKind.kind && object.kind !== paramKind.kind) return false;
    if (paramKind.apiVersion && object.apiVersion !== paramKind.apiVersion) return false;
    return true;
  }
}
