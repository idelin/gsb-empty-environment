// Minimal ambient declarations so editors/tsc can resolve Node builtins
// without @types/node (this project is dependency-free by design).
declare module 'node:*' {
  const anyExport: any;
  export = anyExport;
}
declare var process: any;
declare var Buffer: any;
