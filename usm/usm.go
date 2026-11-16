// Package usm exposes the SNMPv3 USM timeliness checks implemented by the
// parent package under a conventional import name.
package usm

import (
	netsec "example.org/netsec/q0718"
)

const (
	TimeWindow     = netsec.TimeWindow
	MaxEngineBoots = netsec.MaxEngineBoots
	MaxEngineTime  = netsec.MaxEngineTime

	ReportNone                  = netsec.ReportNone
	ReportUnknownEngineID       = netsec.ReportUnknownEngineID
	ReportNotInTimeWindow       = netsec.ReportNotInTimeWindow
	ReportAuthenticationFailure = netsec.ReportAuthenticationFailure
	ReportWrongDigest           = netsec.ReportWrongDigest
	ReportWrongDigests          = netsec.ReportWrongDigests

	UnknownEngineID       = netsec.UnknownEngineID
	NotInTimeWindow       = netsec.NotInTimeWindow
	AuthenticationFailure = netsec.AuthenticationFailure
	WrongDigest           = netsec.WrongDigest

	CodeUnknownEngineID       = netsec.CodeUnknownEngineID
	CodeNotInTimeWindow       = netsec.CodeNotInTimeWindow
	CodeAuthenticationFailure = netsec.CodeAuthenticationFailure
	CodeWrongDigest           = netsec.CodeWrongDigest

	OIDUnsupportedSecLevels = netsec.OIDUnsupportedSecLevels
	OIDNotInTimeWindow      = netsec.OIDNotInTimeWindow
	OIDUnknownUserName      = netsec.OIDUnknownUserName
	OIDUnknownEngineID      = netsec.OIDUnknownEngineID
	OIDWrongDigest          = netsec.OIDWrongDigest
	OIDDecryptionError      = netsec.OIDDecryptionError

	OIDStatsUnsupportedSecLevels = netsec.OIDStatsUnsupportedSecLevels
	OIDStatsNotInTimeWindows     = netsec.OIDStatsNotInTimeWindows
	OIDStatsUnknownUserNames     = netsec.OIDStatsUnknownUserNames
	OIDStatsUnknownEngineIDs     = netsec.OIDStatsUnknownEngineIDs
	OIDStatsWrongDigests         = netsec.OIDStatsWrongDigests
	OIDStatsDecryptionErrors     = netsec.OIDStatsDecryptionErrors
)

type (
	ReportCode = netsec.ReportCode
	Report     = netsec.Report

	State           = netsec.State
	LocalState      = netsec.LocalState
	EngineState     = netsec.EngineState
	Engine          = netsec.Engine
	TimeWindowState = netsec.TimeWindowState

	Message         = netsec.Message
	IncomingMessage = netsec.IncomingMessage
	Request         = netsec.Request
	Packet          = netsec.Packet

	AuthenticateFunc  = netsec.AuthenticateFunc
	UserExistsFunc    = netsec.UserExistsFunc
	UserAuthenticator = netsec.UserAuthenticator
	UserDirectory     = netsec.UserDirectory
	Result            = netsec.Result
)

var (
	ErrUnknownEngineID       = netsec.ErrUnknownEngineID
	ErrNotInTimeWindow       = netsec.ErrNotInTimeWindow
	ErrAuthenticationFailure = netsec.ErrAuthenticationFailure

	UnknownEngineIDReport       = netsec.UnknownEngineIDReport
	NotInTimeWindowReport       = netsec.NotInTimeWindowReport
	AuthenticationFailureReport = netsec.AuthenticationFailureReport
)

func NewState(engineID []byte, engineBoots, engineTime int32) *State {
	return netsec.NewState(engineID, engineBoots, engineTime)
}

func New(engineID []byte, engineBoots, engineTime int32) *State {
	return netsec.New(engineID, engineBoots, engineTime)
}

func NewTimeWindow(engineID []byte, engineBoots, engineTime int32) *State {
	return netsec.NewTimeWindow(engineID, engineBoots, engineTime)
}

func NewEngine(engineID []byte, engineBoots, engineTime int32) *State {
	return netsec.NewEngine(engineID, engineBoots, engineTime)
}

func WindowReport(localBoots, localTime, messageBoots, messageTime int32) Report {
	return netsec.WindowReport(localBoots, localTime, messageBoots, messageTime)
}

func InTimeWindow(localBoots, localTime, messageBoots, messageTime int32) bool {
	return netsec.InTimeWindow(localBoots, localTime, messageBoots, messageTime)
}

func CheckTimeWindow(localBoots, localTime, messageBoots, messageTime int32) bool {
	return netsec.CheckTimeWindow(localBoots, localTime, messageBoots, messageTime)
}

func WithinTimeWindow(localBoots, localTime, messageBoots, messageTime int32) bool {
	return netsec.WithinTimeWindow(localBoots, localTime, messageBoots, messageTime)
}

func IsInTimeWindow(localBoots, localTime, messageBoots, messageTime int32) bool {
	return netsec.IsInTimeWindow(localBoots, localTime, messageBoots, messageTime)
}

func ValidateTimeWindow(localBoots, localTime, messageBoots, messageTime int32) error {
	return netsec.ValidateTimeWindow(localBoots, localTime, messageBoots, messageTime)
}

func Evaluate(msg Message, local any, opts ...any) Result {
	return netsec.Evaluate(msg, local, opts...)
}

func EvaluateWithUsers(msg Message, local any, userExists UserExistsFunc, authenticate AuthenticateFunc) Result {
	return netsec.EvaluateWithUsers(msg, local, userExists, authenticate)
}

func Check(msg Message, local any, opts ...any) Result {
	return netsec.Check(msg, local, opts...)
}

func ProcessMessage(msg Message, local any, opts ...any) Result {
	return netsec.ProcessMessage(msg, local, opts...)
}

func Process(msg Message, local any, opts ...any) Result {
	return netsec.Process(msg, local, opts...)
}
