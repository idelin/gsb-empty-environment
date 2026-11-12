// Package netsec evaluates whether setuid/setgid ELF executables can have a
// shared-library dependency, or an ld.so.preload entry, resolved through a
// directory writable by an unprivileged user.
//
// Analyze reads DT_NEEDED, DT_RPATH, DT_RUNPATH, $ORIGIN and DT_FLAGS_1 with
// debug/elf. The caller supplies the ld.so.cache snapshot, default library
// directories, and a file ownership/permission view. The package follows
// secure-execution search order and reports both the first matching path and
// every unsafe location considered before that match.
package netsec
