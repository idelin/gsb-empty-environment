export function barrier(): { p: Promise<void>; release: () => void } {
  let release: () => void = () => {};
  const p = new Promise<void>((r) => {
    release = r;
  });
  return { p, release };
}

export function deferred<T>(): { p: Promise<T>; resolve: (v: T) => void } {
  let resolve: (v: T) => void = () => {};
  const p = new Promise<T>((r) => {
    resolve = r;
  });
  return { p, resolve };
}
