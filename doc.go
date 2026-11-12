// Package netsec provides admission control for UWB DS-TWR ranging results
// used by a door-access service.
//
// Security properties enforced by Service.Admit:
//
//   - A result must reference a currently registered, open session id.
//   - STS indices are strictly increasing per session; replayed (duplicate)
//     and rolled-back indices are rejected.
//   - Reply latency, including the DS-TWR reply slots Da/Db, must stay within
//     the session-declared limit, defeating delayed-response distance fraud.
//   - The STS quality marker must indicate success.
//   - Timestamps must fall inside the session window and not be zero or
//     implausibly far in the future.
//   - DS-TWR time of flight follows
//     ToF = (Ra*Rb - Da*Db) / (Ra + Rb + Da + Db); a negative ToF, negative
//     timing, or an undefined estimator is rejected.
package netsec
