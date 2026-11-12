package taskpkg

import (
	"example.org/domainreissue/q052/stft"
)

// These aliases expose the STFT helpers from the module root as well as from
// the dedicated stft package.
type (
	Config       = stft.Config
	SpectrumFunc = stft.SpectrumFunc
	Analyzer     = stft.Analyzer
)

var (
	ErrInvalidWindow = stft.ErrInvalidWindow
	ErrInvalidHop    = stft.ErrInvalidHop
	ErrNonHermitian  = stft.ErrNonHermitian
	ErrZeroWindowSum = stft.ErrZeroWindowSum
	ErrFlushed       = stft.ErrFlushed

	New           = stft.New
	NewAnalyzer   = stft.NewAnalyzer
	NewWithConfig = stft.NewWithConfig
	ProcessAll    = stft.ProcessAll
)
