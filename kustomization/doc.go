// Package kustomization implements an admission gate for kustomization.yaml
// files consumed directly by a continuous-delivery build gateway.
//
// The gate is purely standard-library based and works on already-parsed
// documents: callers parse YAML with their decoder of choice and populate a
// Kustomization value. Admit enforces that local references stay inside the
// repository root (including after symlink resolution), remote references use
// https with an allowlisted host and are pinned to 40-character commits,
// generators and transformers cannot invoke exec plugins, Helm charts use
// allowlisted repositories with explicit versions, configMapGenerator files
// obey the same path policy, and reference graphs contain no cycles.
package kustomization
