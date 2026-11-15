// Package netsec models fscrypt policy inheritance and namespace transitions.
//
// A directory may carry an fscrypt policy. Newly created files and directories
// inherit the policy of their parent directory. Renames, hard links, and
// rename exchanges are rejected unless the source and destination parent
// directories have exactly the same policy, including the key identifier,
// policy version, contents and filenames modes, and flags.
//
// The model is intentionally independent of the operating system and performs
// all checks while holding a single lock, so a policy check cannot race with a
// concurrent rename or link operation.
package netsec
