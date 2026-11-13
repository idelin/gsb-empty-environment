// Package netsec implements publication-time resolution and invocation-time
// revalidation for SECURITY DEFINER database functions.
//
// Names in a definer function body are never resolved at execution time.
// Publish freezes table, function, and operator references to
// (ObjectID, Version) through an explicit, read-only-first search path. Invoke
// rechecks every frozen object, every path schema's write policy, and the
// function's revocation state. Host callbacks receive only a frozen ObjectID,
// never a name that could be resolved again.
package netsec
