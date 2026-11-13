// Package netsec provides the job-object restriction policy for the Go
// sandbox host.
//
// Limits combines the Windows job restrictions (active process cap, job
// memory cap, KILL_ON_JOB_CLOSE, BREAKAWAY_OK, SILENT_BREAKAWAY_OK and the
// JOB_OBJECT_UILIMIT_* bits) with process-creation admission rules:
//
//   - SandboxLimits / ValidateSandboxPolicy force KILL_ON_JOB_CLOSE on and
//     both breakaway paths off.
//   - Job.AdmitCreate only honors CREATE_BREAKAWAY_FROM_JOB /
//     CREATE_SILENT_BREAKAWAY_FROM_JOB when the matching allow bit is set.
//   - A create that would exceed MaxActiveProcesses is rejected with
//     ErrProcessLimitExceeded; existing processes are never killed.
//   - NestedLimits / MergeLimits combine parent and child limits using the
//     stricter value (smaller cap, unioned restrictions, AND-ed breakaway
//     capabilities) and reject any attempt to clear the parent's
//     KILL_ON_JOB_CLOSE.
package netsec
