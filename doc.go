// Package netsec provides importable admission checks for rendered
// Helm-style chart templates. It prevents third-party templates from
// reading cluster Secrets through lookup or external-state functions and
// from leaking Secret values into non-Secret rendered resources such as
// ConfigMaps, labels or annotations.
//
// The caller supplies three inputs:
//
//   - the template function calls (name plus static/dynamic argument
//     literals) extracted during template parsing;
//   - the rendered resources with every string field flattened into
//     dotted paths (Flatten and NewResource build these from generic
//     map/slice objects);
//   - a SecretFetcher callback returning the decoded cluster Secret
//     values that rendered output must not expose.
//
// Admit rejects: lookup whose kind is "Secret"; lookup whose kind is a
// dynamic expression and cannot be resolved statically; any forbidden
// external-state function (getHostByName, env, expandenv, readFile); and
// any non-Secret resource string field containing a Secret value (length
// at least DefaultMinSecretLen) in plain, base64 or hexadecimal form.
// Each violation carries the template position or the resource and field
// path so callers can report it in admission responses.
package netsec
