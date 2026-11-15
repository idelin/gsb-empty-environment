// Package netsec audits inherited file descriptors at the entry point of
// a privileged helper: an unprivileged parent may leave sensitive files,
// directories or sockets open above fd 2, or replace the standard
// streams. Sweep classifies each descriptor through caller-injected
// probes as keep, close or reject.
package netsec
