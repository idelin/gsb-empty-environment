// Resolution hook mapping `node:test` to the local shim (Node 16 fallback).
export function resolve(specifier, context, next) {
  if (specifier === "node:test") {
    return {
      url: new URL("./node16-test-shim.mjs", import.meta.url).href,
      shortCircuit: true,
    };
  }
  return next(specifier, context);
}
