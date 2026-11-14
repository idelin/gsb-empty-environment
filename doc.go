// Package netsec provides tenant- and exchange-scoped authorization for topic
// bindings. Binding and routing keys are parsed as dot-separated segments;
// "*" matches exactly one segment and "#" matches zero or more segments.
//
// Authorization is deny-by-default. ACL changes advance a generation and
// revalidate existing subscriptions. Subscriptions are checked again when an
// event is routed and when it is consumed.
package netsec
