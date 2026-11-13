// Package netsec implements a small, host-side Windows token and access-check
// model suitable for reasoning about sandbox restricted tokens.
//
// A Token carries a user SID, enabled/deny-only/disabled groups, enabled
// privileges, an integrity level, and a restricted-SID set. DeriveRestricted
// only tightens a token: enabled groups can move to deny-only or disabled,
// privileges can be deleted, integrity can be lowered, and restricted SIDs
// can be appended. It never lets a caller add an enabled identity or privilege.
//
// CheckAccess walks an ordered DACL. Allow ACEs match the user and enabled
// groups; Deny ACEs additionally match deny-only groups. Disabled groups never
// match. A matching Deny rejects immediately; Allow ACEs accumulate a grant;
// all requested bits must be granted. A non-empty restricted-SID set forces a
// second check in which the restricted SIDs replace the user and groups for
// Allow matching, while deny-only groups remain deny-only. The two grants are
// intersected. An object integrity label above the token's level blocks Write
// and ChangeOwner. Enabled SeBackupPrivilege and SeTakeOwnershipPrivilege
// affect only the AccessBackupRead and AccessTakeOwnership bits respectively.
package netsec
