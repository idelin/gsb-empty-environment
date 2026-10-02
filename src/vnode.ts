/**
 * Virtual node tree and template definitions.
 *
 * Templates are plain data built with `h()`. Dynamic parts are expressed as
 * zero-argument bindings (functions that read signals); the engine subscribes
 * to each binding individually at mount time.
 */
export type Binding<T> = () => T;

export interface VText {
  kind: "text";
  path: string;
  value: string;
}

/** Placeholder marking the slot of a `when()` conditional. */
export interface VAnchor {
  kind: "anchor";
  path: string;
  current: VNode | null;
}

export interface VElement {
  kind: "element";
  path: string;
  tag: string;
  attrs: Record<string, string>;
  children: VNode[];
}

export type VNode = VElement | VText | VAnchor;

export type TextBinding = Binding<string | number | null | undefined>;

export type ChildDef =
  | ElementDef
  | ConditionalDef
  | TextBinding
  | string
  | number
  | boolean
  | null
  | undefined;

export type ComponentFn = (props: Record<string, unknown>) => ElementDef;

export interface ElementDef {
  tag: string | ComponentFn;
  props: Record<string, unknown>;
  children: ChildDef[];
}

export interface ConditionalDef {
  kind: "conditional";
  cond: Binding<unknown>;
  thenDef: () => ChildDef;
  elseDef?: () => ChildDef;
}

export function h(
  tag: string | ComponentFn,
  props?: Record<string, unknown> | null,
  ...children: ChildDef[]
): ElementDef {
  return { tag, props: props ?? {}, children };
}

/** Conditional slot: swaps its content with InsertNode/RemoveNode patches. */
export function when(
  cond: Binding<unknown>,
  thenDef: () => ChildDef,
  elseDef?: () => ChildDef,
): ConditionalDef {
  return { kind: "conditional", cond, thenDef, elseDef };
}
