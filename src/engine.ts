/**
 * The fine-grained update engine.
 *
 * `mount()` walks the template exactly once. Every dynamic binding (text or
 * attribute) becomes its own Computation subscribed only to the signals it
 * actually reads. When a signal changes, the affected computations re-run and
 * emit minimal Patch operations aimed at stable node paths — the component
 * tree is never re-rendered or diffed.
 */
import { Computation } from "./signal.ts";
import { applyPatch, type Patch } from "./patch.ts";
import type {
  Binding,
  ChildDef,
  ConditionalDef,
  ElementDef,
  TextBinding,
  VAnchor,
  VElement,
  VNode,
  VText,
} from "./vnode.ts";

export interface Engine {
  /** Live virtual tree, kept in sync by applying every emitted patch. */
  readonly root: VElement;
  /** Returns the patches generated since the last call (and clears the queue). */
  drainPatches(): Patch[];
  /** Tears down every subscription created by this engine. */
  dispose(): void;
}

function normalizeText(value: string | number | boolean | null | undefined): string {
  if (value === null || value === undefined || typeof value === "boolean") return "";
  return String(value);
}

function isConditional(def: object): def is ConditionalDef {
  return "kind" in def && def.kind === "conditional";
}

export function mount(def: ElementDef): Engine {
  const allComputations = new Set<Computation>();
  const pending: Patch[] = [];
  let root!: VElement;

  const emit = (patch: Patch): void => {
    pending.push(patch);
    applyPatch(root, patch);
  };

  const register = (computation: Computation, scope: Computation[]): void => {
    allComputations.add(computation);
    scope.push(computation);
  };

  const release = (scope: Computation[]): void => {
    for (const computation of scope) {
      computation.dispose();
      allComputations.delete(computation);
    }
    scope.length = 0;
  };

  function mountTextBinding(binding: TextBinding, path: string, scope: Computation[]): VText {
    const node: VText = { kind: "text", path, value: "" };
    let first = true;
    const computation = new Computation(() => {
      const next = normalizeText(binding());
      if (first) {
        node.value = next;
        first = false;
        return;
      }
      if (next !== node.value) {
        emit({ op: "UpdateTextNode", target: path, value: next });
      }
    });
    register(computation, scope);
    computation.run();
    return node;
  }

  function mountAttrBinding(
    el: VElement,
    name: string,
    binding: Binding<unknown>,
    scope: Computation[],
  ): void {
    let first = true;
    const computation = new Computation(() => {
      const raw = binding();
      const next = raw === false || raw === null || raw === undefined ? null : String(raw);
      if (first) {
        if (next !== null) el.attrs[name] = next;
        first = false;
        return;
      }
      if (next === null) {
        if (name in el.attrs) emit({ op: "RemoveAttribute", target: el.path, name });
      } else if (el.attrs[name] !== next) {
        emit({ op: "SetAttribute", target: el.path, name, value: next });
      }
    });
    register(computation, scope);
    computation.run();
  }

  function mountConditional(def: ConditionalDef, path: string, scope: Computation[]): VAnchor {
    const anchor: VAnchor = { kind: "anchor", path, current: null };
    let branchScope: Computation[] = [];
    let first = true;
    const computation = new Computation(() => {
      const branch = def.cond() ? def.thenDef : def.elseDef;
      const childDef = branch === undefined ? null : branch();
      if (first) {
        anchor.current = childDef === null ? null : mountChild(childDef, `${path}/0`, branchScope);
        first = false;
        return;
      }
      release(branchScope);
      if (anchor.current !== null) {
        emit({ op: "RemoveNode", target: path });
      }
      if (childDef !== null) {
        const node = mountChild(childDef, `${path}/0`, branchScope);
        emit({ op: "InsertNode", target: path, node });
      }
    });
    register(computation, scope);
    computation.run();
    return anchor;
  }

  function mountElement(def: ElementDef, path: string, scope: Computation[]): VElement {
    if (typeof def.tag === "function") {
      // Components run exactly once at mount; updates flow through bindings,
      // never through re-invoking the component or diffing its output.
      const inner = def.tag({ ...def.props, children: def.children });
      return mountElement(inner, path, scope);
    }
    const el: VElement = { kind: "element", path, tag: def.tag, attrs: {}, children: [] };
    for (const [name, value] of Object.entries(def.props)) {
      if (typeof value === "function") {
        mountAttrBinding(el, name, value as Binding<unknown>, scope);
      } else if (value !== false && value !== null && value !== undefined) {
        el.attrs[name] = String(value);
      }
    }
    def.children.forEach((child, index) => {
      el.children.push(mountChild(child, `${path}/${index}`, scope));
    });
    return el;
  }

  function mountChild(child: ChildDef, path: string, scope: Computation[]): VNode {
    if (typeof child === "function") return mountTextBinding(child, path, scope);
    if (typeof child === "object" && child !== null) {
      if (isConditional(child)) return mountConditional(child, path, scope);
      return mountElement(child, path, scope);
    }
    return { kind: "text", path, value: normalizeText(child) };
  }

  const rootScope: Computation[] = [];
  root = mountElement(def, "0", rootScope);

  return {
    root,
    drainPatches(): Patch[] {
      return pending.splice(0, pending.length);
    },
    dispose(): void {
      release([...allComputations]);
    },
  };
}
