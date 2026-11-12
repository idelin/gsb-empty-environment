// Package netsec evaluates signature-verified DDS Security permissions
// documents for an access-control plugin.
//
// The caller is responsible for verifying the XML signature on the document
// (for example, with the Permissions CA's public key). This package then:
//
//   - selects the grant whose subject_name matches the participant's identity
//     certificate subject;
//   - rejects a grant outside its not_before/not_after interval as a whole;
//   - evaluates allow_rule and deny_rule entries in document order, where the
//     first matching rule wins;
//   - matches domain IDs, partitions, publish/subscribe topics, and returns
//     Deny when no rule matches.
//
// Topic and partition expressions use case-sensitive POSIX fnmatch-style
// wildcards. A '*' also matches '/', so patterns work for DDS/ROS topic names
// such as "rt/chatter".
package netsec
