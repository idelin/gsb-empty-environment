// Package netsec implements Kubernetes-style request impersonation
// authorization for an HTTP API server.
//
// An authenticated requestor may present Impersonate-User,
// Impersonate-Group, Impersonate-Uid and Impersonate-Extra-<key> headers.
// Every impersonated object is authorized independently with verb
// "impersonate" against resources users, groups, serviceaccounts, uids and
// userextras/<key>; resourceNames may further constrain a rule. If any check
// fails the whole request is rejected with HTTP 403. On success the
// impersonated identity replaces the requestor for all downstream
// authorization decisions (the requestor's permissions are not carried
// forward) and both identities are written to the audit sink.
package netsec
