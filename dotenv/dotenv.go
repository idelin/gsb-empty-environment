// Package dotenv safely parses and applies .env files.
package dotenv

import (
	"io"

	netsec "example.org/netsec/q0595"
)

const (
	// MaxFileSize is the largest .env file that will be accepted.
	MaxFileSize = netsec.MaxFileSize
	// MaxLines is the largest number of physical lines that will be accepted.
	MaxLines = netsec.MaxLines
)

var (
	// ErrFileTooLarge is returned when the input exceeds MaxFileSize.
	ErrFileTooLarge = netsec.ErrFileTooLarge
	// ErrTooManyLines is returned when the input exceeds MaxLines.
	ErrTooManyLines = netsec.ErrTooManyLines
	// ErrNUL is returned when a value contains a NUL byte.
	ErrNUL = netsec.ErrNUL
	// ErrProtected is returned for variables that can alter process behavior.
	ErrProtected = netsec.ErrProtected
	// ErrInvalidSyntax is returned for malformed dotenv input.
	ErrInvalidSyntax = netsec.ErrInvalidSyntax
	// ErrBOM is returned when a UTF-8 BOM appears anywhere except the start.
	ErrBOM = netsec.ErrBOM
)

// ParseError describes an error at a particular line in a dotenv file.
type ParseError = netsec.ParseError

// Parse validates data and returns the environment variables it defines.
func Parse(data []byte, protected ...string) (map[string]string, error) {
	return netsec.Parse(data, protected...)
}

// ParseReader reads and validates a dotenv stream.
func ParseReader(r io.Reader, protected ...string) (map[string]string, error) {
	return netsec.ParseReader(r, protected...)
}

// ParseFile reads and validates a dotenv file without changing the process
// environment.
func ParseFile(path string, protected ...string) (map[string]string, error) {
	return netsec.ParseFile(path, protected...)
}

// LoadReader validates and then applies a dotenv stream to the process
// environment.
func LoadReader(r io.Reader, protected ...string) error {
	return netsec.LoadReader(r, protected...)
}

// LoadFile validates and then applies a dotenv file to the process
// environment.
func LoadFile(path string, protected ...string) error {
	return netsec.LoadFile(path, protected...)
}

// Load is an alias for LoadFile.
func Load(path string, protected ...string) error {
	return netsec.LoadFile(path, protected...)
}
