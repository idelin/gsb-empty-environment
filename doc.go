// Package netsec audits the global object graph handed to a sandboxed Lua
// environment embedded in a Go host.
//
// A sandbox constructor normally restricts user scripts by replacing the
// global environment table, but Lua keeps several side channels open:
// shared metatables (string, number, ...), metatable __index forwarding,
// function upvalues and the load/loadfile/dofile/require family. Any of
// them can re-expose debug, io, os.execute or string.dump.
//
// The host supplies the extracted object graph (tables, functions, their
// metatables, field edges, upvalue edges and builtin identifiers). Audit
// runs a multi-source shortest-path search starting from the sandbox
// environment table and the shared metatables, and returns every reachable
// dangerous sink together with a shortest access path. A report with no
// findings means the environment is sealed.
package netsec
