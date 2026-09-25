import { isRefMarker, parsePointer, REF_KEY, type JsonValue, type PathSeg } from "./json.ts";

export { DELETE_KEY, isDeleteSentinel } from "./json.ts";

export { REF_KEY } from "./json.ts";

export interface RefMarker {
  [REF_KEY]: string;
}

export function isRef(v: JsonValue): v is JsonValue & RefMarker {
  return isRefMarker(v);
}

export function refTarget(v: JsonValue): PathSeg[] {
  if (!isRef(v)) throw new Error("not a ref marker");
  return parsePointer((v as unknown as RefMarker)[REF_KEY]);
}
