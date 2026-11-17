// Package netsec implements atomic, label-authorized resource creation.
//
// A Host validates the final merge of requested, inherited, and mandatory
// labels before creating an invisible staged object. The object becomes visible
// only after its labels and final digest have been persisted and publication
// succeeds. Readers never observe an object in an intermediate authorization
// state, and permission checks are bound to the final label digest.
package netsec
