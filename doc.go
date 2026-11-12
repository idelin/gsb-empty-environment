// Package netsec provides a policy guard for an IMDSv2 metadata forwarding
// proxy. It validates token acquisition, binds tokens to source containers,
// and rejects forwarding headers and unsafe metadata paths before a request is
// relayed to 169.254.169.254.
package netsec
