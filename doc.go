// Package netsec normalizes Memcached requests for a multi-tenant prefix
// proxy. It validates keys and storage-block framing before forwarding,
// locks one wire protocol per connection, and filters tenant keys from
// upstream responses.
package netsec
