/** Minimal ambient declarations so `tsc --noEmit` works without @types/node. */
declare const console: {
  log(message: string): void;
  error(message: string): void;
};
