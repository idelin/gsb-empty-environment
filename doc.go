// Package netsec provides an importable executor router for a CI scheduling
// service.
//
// The router treats job labels as selectors, never as proof of trust. A job's
// effective trust is derived from the repository policy held by the scheduler,
// the job's provenance, and the branch named by its ref. Privileged labels and
// capabilities are granted only to protected branches of privileged
// repositories whose provenance is not a fork.
//
// Executors declare a trust domain at registration time. A job can only use an
// executor in the single domain derived for that job; labels and capabilities
// never allow an executor to be borrowed from another domain. Queue names,
// cache namespaces, and artifact namespaces returned with a placement carry
// the same domain scope.
package netsec
