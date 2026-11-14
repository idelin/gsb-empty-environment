// Package netsec provides an importable data-subject-request coordinator for
// deletion and export workloads.
//
// A Tracker records every request against a subject, a data scope and a
// deadline. It verifies the caller's identity before accepting the request and
// requires a valid completion receipt from every registered copy system (primary
// database, backups, caches, search indexes and derived systems) before a
// request can be reported complete. Incomplete systems are retried according
// to policy and overdue requests raise alerts.
package netsec
