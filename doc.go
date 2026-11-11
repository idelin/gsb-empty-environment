// Package netsec is an incremental decision engine for cloud security groups.
//
// Security group members are address sets. A Rule references a source group
// and matches a destination port inside a target group; rules combine by
// union. Membership and rule changes are staged in a Txn and published by
// Engine.Apply as one atomic transaction. Queries return every minimal
// Evidence that allows a flow, not just the final allow decision.
//
// Rule references stay one hop, so cyclic group references are legal.
// Engine.CheckRebuild recomputes all derived state from ground facts and
// audits reference cycles, empty groups and addresses shared by multiple
// groups. The package exposes no management backend.
package netsec
