// Package netsec evaluates cloud IAM policy sets and computes privilege
// escalation paths built from iam:PassRole combined with creating and
// invoking Lambda functions.
//
// The model supports Allow/Deny statements, wildcard matching for Action and
// Resource, the iam:PassedToService condition, and role trust policies.
package netsec
