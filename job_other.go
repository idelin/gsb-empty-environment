//go:build !windows

package netsec

// On non-Windows platforms platformJob (declared in job.go) stays nil and the
// kernel-backed constructors in job_windows.go are not compiled.
