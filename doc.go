// Package netsec implements request-scoped multi-stage HTTP responses for
// reverse proxies that forward 103 Early Hints before a final response.
//
// A MultiStageResponse belongs to exactly one request instance. Each hint is
// copied and filtered before it is handed to the response pipeline, and the
// handle becomes terminal as soon as a final response is committed or the
// request is canceled. Consequently, a delayed hint can never be attached to
// a later request or merged into the final response's headers.
package netsec
