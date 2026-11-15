// Package netsec detects unsafe use of JNI primitive-array critical sections.
//
// A native JNI integration emits Get, Release, JNI-call, blocking-call, and
// pointer-access events into a Detector. The detector tracks critical regions
// per thread and reports JNI calls or blocking operations inside a region,
// out-of-bounds pointer accesses, use after release, duplicate releases,
// regions still pinned at thread exit, and writes discarded by JNI_ABORT.
//
// The package is a pure Go trace consumer. It does not intercept JNI by
// itself; the host must feed events from its JNI wrapper, JVMTI agent, uprobes,
// or another tracing mechanism through Observe or the convenience methods.
package netsec
