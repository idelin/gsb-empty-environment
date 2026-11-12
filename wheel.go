package netsec

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode"
)

// ValidateWheel validates a wheel archive. It accepts a path, a byte slice,
// an io.Reader, a *zip.Reader, or a *zip.ReadCloser.
//
// An optional filename may be supplied when the input is not a path. When a
// filename is available it is also checked against METADATA and WHEEL.
func ValidateWheel(input any, filename ...string) error {
	name := ""
	if len(filename) > 0 {
		name = filename[0]
	}

	switch value := input.(type) {
	case string:
		return ValidateWheelFile(value)
	case []byte:
		return validateWheelBytes(value, name)
	case io.Reader:
		data, err := io.ReadAll(value)
		if err != nil {
			return fmt.Errorf("netsec: read wheel: %w", err)
		}
		return validateWheelBytes(data, name)
	case *zip.Reader:
		if value == nil {
			return fmt.Errorf("netsec: nil zip reader")
		}
		return validateZip(value, name)
	case *zip.ReadCloser:
		if value == nil {
			return fmt.Errorf("netsec: nil zip reader")
		}
		return validateZip(&value.Reader, name)
	case zip.ReadCloser:
		return validateZip(&value.Reader, name)
	case nil:
		return fmt.Errorf("netsec: nil wheel input")
	default:
		return fmt.Errorf("netsec: unsupported wheel input type %T", input)
	}
}

// Validate is an alias for ValidateWheel.
func Validate(input any, filename ...string) error {
	return ValidateWheel(input, filename...)
}

// VerifyWheel is an alias for ValidateWheel.
func VerifyWheel(input any, filename ...string) error {
	return ValidateWheel(input, filename...)
}

// CheckWheel is an alias for ValidateWheel.
func CheckWheel(input any, filename ...string) error {
	return ValidateWheel(input, filename...)
}

// ValidateWheelFile validates a wheel stored at filename.
func ValidateWheelFile(filename string) error {
	reader, err := zip.OpenReader(filename)
	if err != nil {
		return fmt.Errorf("netsec: open wheel %q: %w", filename, err)
	}
	defer reader.Close()

	return validateZip(&reader.Reader, filename)
}

// ValidateWheelBytes validates an in-memory wheel. filename is optional and is
// used to check the wheel filename tags and distribution metadata.
func ValidateWheelBytes(data []byte, filename ...string) error {
	name := ""
	if len(filename) > 0 {
		name = filename[0]
	}
	return validateWheelBytes(data, name)
}

// ValidateWheelReader reads a wheel from r and validates it. filename is
// optional and is used for the filename checks.
func ValidateWheelReader(r io.Reader, filename ...string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("netsec: read wheel: %w", err)
	}
	return validateWheelBytes(data, firstString(filename))
}

// ValidateZipReader validates an already-opened zip archive. filename is
// optional and is used for the filename checks.
func ValidateZipReader(r *zip.Reader, filename ...string) error {
	if r == nil {
		return fmt.Errorf("netsec: nil zip reader")
	}
	return validateZip(r, firstString(filename))
}

// NormalizeName applies the PEP 503-style name normalization used by wheel
// metadata: lower-case the name and collapse runs of '-', '_' and '.' into one
// '-'.
func NormalizeName(name string) string {
	name = strings.ToLower(name)

	var b strings.Builder
	b.Grow(len(name))
	separator := false
	for _, r := range name {
		switch r {
		case '-', '_', '.':
			if !separator {
				b.WriteByte('-')
				separator = true
			}
		default:
			b.WriteRune(r)
			separator = false
		}
	}
	return b.String()
}

// Normalize is an alias for NormalizeName.
func Normalize(name string) string {
	return NormalizeName(name)
}

func validateWheelBytes(data []byte, filename string) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("netsec: open wheel: %w", err)
	}
	return validateZip(reader, filename)
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func validateZip(reader *zip.Reader, filename string) error {
	if len(reader.File) == 0 {
		return fmt.Errorf("netsec: empty wheel archive")
	}

	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		if err := validateEntryName(file.Name); err != nil {
			return err
		}
		if _, exists := files[file.Name]; exists {
			return fmt.Errorf("netsec: duplicate archive entry %q", file.Name)
		}
		files[file.Name] = file
	}

	distInfo, err := findDistInfo(files)
	if err != nil {
		return err
	}

	metadata, err := readMetadata(files, distInfo)
	if err != nil {
		return err
	}

	metadataName, err := singleHeader(metadata, "name")
	if err != nil {
		return err
	}
	metadataVersion, err := singleHeader(metadata, "version")
	if err != nil {
		return err
	}

	if err := checkDistInfoName(distInfo, metadataName, metadataVersion); err != nil {
		return err
	}

	manifestTags, err := readWheelTags(files, distInfo)
	if err != nil {
		return err
	}

	if filename != "" {
		if err := checkWheelFilename(filename, metadataName, metadataVersion, manifestTags); err != nil {
			return err
		}
	}

	return checkRecord(files, distInfo)
}

func validateEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("netsec: archive contains an empty entry name")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("netsec: archive entry %q contains a NUL byte", name)
	}
	if strings.ContainsRune(name, '\\') {
		return fmt.Errorf("netsec: archive entry %q contains a backslash", name)
	}
	if isAbsoluteArchivePath(name) {
		return fmt.Errorf("netsec: archive entry %q is an absolute path", name)
	}

	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return fmt.Errorf("netsec: archive entry %q contains a parent-directory component", name)
		}
	}
	return nil
}

func isAbsoluteArchivePath(name string) bool {
	if path.IsAbs(name) {
		return true
	}
	if len(name) >= 2 && name[1] == ':' && isDriveLetter(name[0]) {
		return true
	}
	return false
}

func isDriveLetter(r byte) bool {
	return r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

func findDistInfo(files map[string]*zip.File) (string, error) {
	var candidates []string
	for name := range files {
		first, _, hasSlash := strings.Cut(name, "/")
		if !hasSlash || !strings.HasSuffix(first, ".dist-info") {
			continue
		}
		if first == ".dist-info" {
			return "", fmt.Errorf("netsec: invalid .dist-info directory name %q", first)
		}
		if !contains(candidates, first) {
			candidates = append(candidates, first)
		}
	}

	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("netsec: wheel has no .dist-info directory")
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("netsec: wheel has multiple .dist-info directories: %s", strings.Join(candidates, ", "))
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func readMetadata(files map[string]*zip.File, distInfo string) (map[string][]string, error) {
	file, ok := files[distInfo+"/METADATA"]
	if !ok {
		return nil, fmt.Errorf("netsec: missing %s/METADATA", distInfo)
	}
	data, err := readZipFile(file)
	if err != nil {
		return nil, fmt.Errorf("netsec: read METADATA: %w", err)
	}
	headers, err := parseHeaders(data)
	if err != nil {
		return nil, fmt.Errorf("netsec: parse METADATA: %w", err)
	}
	return headers, nil
}

func readWheelTags(files map[string]*zip.File, distInfo string) (map[string]struct{}, error) {
	file, ok := files[distInfo+"/WHEEL"]
	if !ok {
		return nil, fmt.Errorf("netsec: missing %s/WHEEL", distInfo)
	}
	data, err := readZipFile(file)
	if err != nil {
		return nil, fmt.Errorf("netsec: read WHEEL: %w", err)
	}
	headers, err := parseHeaders(data)
	if err != nil {
		return nil, fmt.Errorf("netsec: parse WHEEL: %w", err)
	}

	values := headers["tag"]
	if len(values) == 0 {
		return nil, fmt.Errorf("netsec: WHEEL has no Tag headers")
	}

	tags := make(map[string]struct{}, len(values))
	for _, value := range values {
		tag := strings.TrimSpace(value)
		if tag == "" {
			return nil, fmt.Errorf("netsec: WHEEL contains an empty Tag")
		}
		tags[tag] = struct{}{}
	}
	return tags, nil
}

func singleHeader(headers map[string][]string, key string) (string, error) {
	values := headers[key]
	if len(values) != 1 {
		return "", fmt.Errorf("netsec: expected exactly one %s header, found %d", headerLabel(key), len(values))
	}
	value := strings.TrimSpace(values[0])
	if value == "" {
		return "", fmt.Errorf("netsec: empty %s header", headerLabel(key))
	}
	return value, nil
}

func headerLabel(key string) string {
	if key == "" {
		return key
	}
	return strings.ToUpper(key[:1]) + key[1:]
}

func parseHeaders(data []byte) (map[string][]string, error) {
	headers := make(map[string][]string)
	lastKey := ""
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			if lastKey == "" {
				return nil, fmt.Errorf("continuation line without a preceding header")
			}
			values := headers[lastKey]
			values[len(values)-1] = strings.TrimSpace(values[len(values)-1] + " " + strings.TrimSpace(line))
			continue
		}

		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("malformed header line %q", line)
		}
		key := strings.ToLower(strings.TrimSpace(line[:colon]))
		if key == "" {
			return nil, fmt.Errorf("malformed header line %q", line)
		}
		value := strings.TrimSpace(line[colon+1:])
		headers[key] = append(headers[key], value)
		lastKey = key
	}
	return headers, nil
}

func checkDistInfoName(distInfo, metadataName, metadataVersion string) error {
	base := strings.TrimSuffix(distInfo, ".dist-info")
	separator := "-" + metadataVersion
	if len(base) <= len(separator) || !strings.HasSuffix(base, separator) {
		return fmt.Errorf("netsec: .dist-info directory %q does not end with the METADATA version %q", distInfo, metadataVersion)
	}
	namePart := base[:len(base)-len(separator)]
	if namePart == "" {
		return fmt.Errorf("netsec: invalid .dist-info directory name %q", distInfo)
	}
	if NormalizeName(namePart) != NormalizeName(metadataName) {
		return fmt.Errorf("netsec: .dist-info name %q does not match METADATA Name %q", namePart, metadataName)
	}
	return nil
}

func checkWheelFilename(filename, metadataName, metadataVersion string, manifestTags map[string]struct{}) error {
	base := path.Base(filename)
	if !strings.HasSuffix(strings.ToLower(base), ".whl") {
		return nil
	}

	parts := strings.Split(base[:len(base)-4], "-")
	if len(parts) < 5 {
		return fmt.Errorf("netsec: invalid wheel filename %q", base)
	}
	tagParts := parts[len(parts)-3:]
	for _, part := range tagParts {
		if part == "" {
			return fmt.Errorf("netsec: invalid wheel filename %q", base)
		}
	}
	if err := checkFilenameIdentity(parts[:len(parts)-3], metadataName, metadataVersion); err != nil {
		return err
	}

	fileTags, err := expandWheelTags(tagParts[0], tagParts[1], tagParts[2])
	if err != nil {
		return err
	}
	if !sameTagSet(fileTags, manifestTags) {
		return fmt.Errorf("netsec: wheel filename tags %s do not match WHEEL tags %s", formatTagSet(fileTags), formatTagSet(manifestTags))
	}
	return nil
}

func checkFilenameIdentity(prefixParts []string, metadataName, metadataVersion string) error {
	if len(prefixParts) < 2 {
		return fmt.Errorf("netsec: invalid wheel filename")
	}

	candidates := []string{strings.Join(prefixParts, "-")}
	if len(prefixParts) > 2 {
		candidates = append(candidates, strings.Join(prefixParts[:len(prefixParts)-1], "-"))
	}

	versionSeen := false
	for _, candidate := range candidates {
		for _, version := range []string{metadataVersion, strings.ReplaceAll(metadataVersion, "-", "_")} {
			suffix := "-" + version
			if version == "" || !strings.HasSuffix(candidate, suffix) {
				continue
			}
			versionSeen = true
			distribution := candidate[:len(candidate)-len(suffix)]
			if distribution == "" {
				continue
			}
			if NormalizeName(distribution) == NormalizeName(metadataName) {
				return nil
			}
		}
	}

	if !versionSeen {
		return fmt.Errorf("netsec: wheel filename version does not match METADATA Version %q", metadataVersion)
	}
	return fmt.Errorf("netsec: wheel filename name does not match METADATA Name %q", metadataName)
}

func expandWheelTags(python, abi, platform string) (map[string]struct{}, error) {
	pythons := strings.Split(python, ".")
	abis := strings.Split(abi, ".")
	platforms := strings.Split(platform, ".")
	if len(pythons) == 0 || len(abis) == 0 || len(platforms) == 0 {
		return nil, fmt.Errorf("netsec: invalid wheel tag set")
	}

	tags := make(map[string]struct{}, len(pythons)*len(abis)*len(platforms))
	for _, py := range pythons {
		for _, ab := range abis {
			for _, plat := range platforms {
				if !validTagPart(py) || !validTagPart(ab) || !validTagPart(plat) {
					return nil, fmt.Errorf("netsec: invalid wheel tag component")
				}
				tags[py+"-"+ab+"-"+plat] = struct{}{}
			}
		}
	}
	return tags, nil
}

func validTagPart(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func sameTagSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for tag := range left {
		if _, ok := right[tag]; !ok {
			return false
		}
	}
	return true
}

func formatTagSet(tags map[string]struct{}) string {
	values := make([]string, 0, len(tags))
	for tag := range tags {
		values = append(values, tag)
	}
	sortStrings(values)
	return strings.Join(values, ", ")
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func checkRecord(files map[string]*zip.File, distInfo string) error {
	recordName := distInfo + "/RECORD"
	recordFile, ok := files[recordName]
	if !ok {
		return fmt.Errorf("netsec: missing %s", recordName)
	}

	data, err := readZipFile(recordFile)
	if err != nil {
		return fmt.Errorf("netsec: read RECORD: %w", err)
	}

	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("netsec: parse RECORD: %w", err)
	}

	recorded := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if len(row) < 3 {
			return fmt.Errorf("netsec: RECORD row has %d fields, want at least 3", len(row))
		}
		entryName := row[0]
		if err := validateEntryName(entryName); err != nil {
			return fmt.Errorf("netsec: invalid RECORD path: %w", err)
		}
		if _, duplicate := recorded[entryName]; duplicate {
			return fmt.Errorf("netsec: duplicate RECORD entry %q", entryName)
		}
		recorded[entryName] = struct{}{}

		file, exists := files[entryName]
		if !exists {
			return fmt.Errorf("netsec: RECORD lists missing archive entry %q", entryName)
		}
		if file.FileInfo().IsDir() {
			return fmt.Errorf("netsec: RECORD lists directory %q", entryName)
		}

		if isRecordOrSignature(entryName, distInfo) {
			continue
		}
		if err := verifyRecordRow(file, row); err != nil {
			return fmt.Errorf("netsec: RECORD entry %q: %w", entryName, err)
		}
	}

	for name, file := range files {
		if file.FileInfo().IsDir() || isRecordOrSignature(name, distInfo) {
			continue
		}
		if _, ok := recorded[name]; !ok {
			return fmt.Errorf("netsec: archive entry %q is not listed in RECORD", name)
		}
	}
	return nil
}

func isRecordOrSignature(name, distInfo string) bool {
	if name == distInfo+"/RECORD" {
		return true
	}
	prefix := distInfo + "/"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	switch strings.TrimPrefix(name, prefix) {
	case "RECORD.jws", "RECORD.p7s":
		return true
	default:
		return false
	}
}

func verifyRecordRow(file *zip.File, row []string) error {
	hashField := row[1]
	if !strings.HasPrefix(hashField, "sha256=") {
		return fmt.Errorf("missing sha256= hash")
	}
	encoded := strings.TrimPrefix(hashField, "sha256=")
	if encoded == "" {
		return fmt.Errorf("empty sha256 hash")
	}
	if strings.ContainsRune(encoded, '=') {
		return fmt.Errorf("sha256 hash %q is padded", encoded)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("sha256 hash is not unpadded URL-safe base64: %w", err)
	}
	if len(decoded) != sha256.Size {
		return fmt.Errorf("sha256 hash has %d bytes, want %d", len(decoded), sha256.Size)
	}

	data, err := readZipFile(file)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}
	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], decoded) {
		return fmt.Errorf("sha256 hash mismatch")
	}

	size, err := strconv.ParseInt(strings.TrimSpace(row[2]), 10, 64)
	if err != nil || size < 0 {
		return fmt.Errorf("invalid size %q", row[2])
	}
	if size != int64(len(data)) {
		return fmt.Errorf("size is %d bytes, RECORD says %d", len(data), size)
	}
	return nil
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
