// Package netsec quantifies how much an adaptive side-channel interaction can
// reveal about a hidden secret.
//
// Solve takes rational prior probabilities over up to MaxSecrets hypotheses,
// up to MaxProbes interaction channels with at most MaxResponses outcomes
// each, and a horizon of at most MaxRounds rounds. Responses in different
// rounds are conditionally independent given the secret. It performs an exact
// belief-state dynamic program using math/big.Rat throughout (no floating
// point) and returns:
//
//   - the maximum achievable probability of guessing the secret correctly,
//     including the option to stop early;
//   - an optimal adaptive strategy tree whose internal nodes select probes,
//     whose branches carry exact response probabilities and posteriors, and
//     whose leaves commit to a secret.
//
// Zero-probability branches are pruned automatically and probes may be
// repeated. The package tests cross-check the dynamic program against full
// enumeration of every adaptive policy on small instances.
package netsec
