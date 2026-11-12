// Package netsec provides write admission control for LwM2M bootstrap
// sessions. A Guard admits mutations only from sessions whose peer matches a
// bootstrap server already trusted by the client, preserves bootstrap
// accounts across root-level deletes, rejects sessions once their lifetime
// elapses, and commits Bootstrap-Finish only when at least one management
// server account uses a security mode other than NoSec. Any failed finish
// rolls the resource store back to its pre-session state.
package netsec
