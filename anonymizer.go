package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultUIDRoot         = "2.25"
	defaultDateShiftDays   = 3650
	deidentificationMethod = "UID remap; date shift; private tags removed; annotations cleared"
)

var (
	tagPatientIdentityRemoved = Tag{0x0012, 0x0062}
	tagDeidentificationMethod = Tag{0x0012, 0x0063}
	tagTemporalModified       = Tag{0x0028, 0x0303}
	tagBurnedInAnnotation     = Tag{0x0028, 0x0301}
)

var (
	// ErrInvalidDICOM is returned when the input is not a supported DICOM
	// dataset or file.
	ErrInvalidDICOM = errInvalidDICOM

	// ErrBurnedInAnnotation is returned when an image declares burned-in
	// annotations and the anonymizer is configured to reject them.
	ErrBurnedInAnnotation = errors.New("DICOM declares burned-in pixel annotations")

	// ErrInvalidUIDRoot is returned for a UID root that cannot produce valid
	// DICOM UIDs.
	ErrInvalidUIDRoot = errors.New("invalid DICOM UID root")
)

// Policy describes the non-default behavior of an Anonymizer.
//
// PrivateTagWhitelist contains exact private tags that may be retained. A
// retained private sequence is still traversed and its contents are cleaned.
// UIDRoot and Secret make UID and date replacement deterministic across
// processes. If Secret is empty, a random per-Anonymizer secret is generated.
type Policy struct {
	PrivateTagWhitelist     []Tag
	UIDRoot                 string
	Secret                  []byte
	DateShiftRangeDays      int
	AllowBurnedInAnnotation bool
	PreservePreamble        bool
}

// DefaultPolicy returns the privacy-preserving default policy.
func DefaultPolicy() Policy {
	return Policy{
		UIDRoot:            defaultUIDRoot,
		DateShiftRangeDays: defaultDateShiftDays,
	}
}

type config struct {
	privateWhitelist map[Tag]struct{}
	privateCreators  map[Tag]struct{}
	uidRoot          string
	secret           []byte
	dateShiftRange   int
	rejectBurnedIn   bool
	preservePreamble bool
}

// Option configures an Anonymizer.
type Option func(*config) error

// WithPrivateTagWhitelist allows exact private tags to be retained.
func WithPrivateTagWhitelist(tags ...Tag) Option {
	return func(c *config) error {
		for _, tag := range tags {
			if !tag.isPrivate() {
				return fmt.Errorf("tag %s is not a private tag", tag)
			}
			c.privateWhitelist[tag] = struct{}{}
			if tag.Element >= 0x1000 {
				c.privateCreators[tag.privateCreatorForData()] = struct{}{}
			}
		}
		return nil
	}
}

// WithPrivateTagWhitelistSpecs allows private tags such as "0009,1001" or
// "(0009,1001)" to be retained.
func WithPrivateTagWhitelistSpecs(specs ...string) Option {
	return func(c *config) error {
		for _, spec := range specs {
			tag, err := ParseTag(spec)
			if err != nil {
				return err
			}
			if !tag.isPrivate() {
				return fmt.Errorf("tag %s is not a private tag", tag)
			}
			c.privateWhitelist[tag] = struct{}{}
			if tag.Element >= 0x1000 {
				c.privateCreators[tag.privateCreatorForData()] = struct{}{}
			}
		}
		return nil
	}
}

// WithUIDRoot sets the root used for replacement UIDs.
func WithUIDRoot(root string) Option {
	return func(c *config) error {
		if err := validateUIDRoot(root); err != nil {
			return err
		}
		c.uidRoot = strings.TrimSuffix(root, ".")
		return nil
	}
}

// WithSecret sets the HMAC key used for UID and date replacement. Reuse the
// same key when multiple exports must share consistent pseudonyms.
func WithSecret(secret []byte) Option {
	return func(c *config) error {
		if len(secret) == 0 {
			return errors.New("anonymization secret must not be empty")
		}
		c.secret = append([]byte(nil), secret...)
		return nil
	}
}

// WithDateShiftRangeDays sets the symmetric per-patient date shift range.
func WithDateShiftRangeDays(days int) Option {
	return func(c *config) error {
		if days <= 0 {
			return errors.New("date shift range must be positive")
		}
		if days > 36500 {
			return errors.New("date shift range is unreasonably large")
		}
		c.dateShiftRange = days
		return nil
	}
}

// WithRejectBurnedInAnnotation controls whether images declaring
// BurnedInAnnotation=YES are rejected. Rejection is enabled by default
// because metadata cleanup cannot remove identity burned into pixels.
func WithRejectBurnedInAnnotation(reject bool) Option {
	return func(c *config) error {
		c.rejectBurnedIn = reject
		return nil
	}
}

// WithPreamblePreserved controls whether the 128-byte DICOM preamble is
// preserved. It is zeroed by default because it is unconstrained data and
// can contain arbitrary leftover bytes.
func WithPreamblePreserved(preserve bool) Option {
	return func(c *config) error {
		c.preservePreamble = preserve
		return nil
	}
}

func defaultConfig() config {
	return config{
		privateWhitelist: make(map[Tag]struct{}),
		privateCreators:  make(map[Tag]struct{}),
		uidRoot:          defaultUIDRoot,
		dateShiftRange:   defaultDateShiftDays,
		rejectBurnedIn:   true,
	}
}

func applyPolicy(c *config, policy Policy) error {
	if policy.UIDRoot != "" {
		if err := WithUIDRoot(policy.UIDRoot)(c); err != nil {
			return err
		}
	}
	if policy.Secret != nil {
		if err := WithSecret(policy.Secret)(c); err != nil {
			return err
		}
	}
	if policy.DateShiftRangeDays != 0 {
		if err := WithDateShiftRangeDays(policy.DateShiftRangeDays)(c); err != nil {
			return err
		}
	}
	if len(policy.PrivateTagWhitelist) != 0 {
		if err := WithPrivateTagWhitelist(policy.PrivateTagWhitelist...)(c); err != nil {
			return err
		}
	}
	if policy.AllowBurnedInAnnotation {
		c.rejectBurnedIn = false
	}
	if policy.PreservePreamble {
		c.preservePreamble = true
	}
	return nil
}

// Anonymizer replaces patient identifiers, private tags, instance UIDs, and
// dates. It is safe for concurrent use. Reuse the same Anonymizer for files
// that belong to the same export so that cross-file UID references remain
// consistent.
type Anonymizer struct {
	config config

	mu       sync.Mutex
	uidRemap map[string]string
}

// NewAnonymizer constructs an anonymizer with privacy-preserving defaults.
func NewAnonymizer(opts ...Option) (*Anonymizer, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	return newAnonymizerFromConfig(cfg)
}

// NewAnonymizerWithPolicy constructs an anonymizer from a Policy.
func NewAnonymizerWithPolicy(policy Policy) (*Anonymizer, error) {
	cfg := defaultConfig()
	if err := applyPolicy(&cfg, policy); err != nil {
		return nil, err
	}
	return newAnonymizerFromConfig(cfg)
}

// MustNewAnonymizer is like NewAnonymizer but panics on invalid options.
func MustNewAnonymizer(opts ...Option) *Anonymizer {
	a, err := NewAnonymizer(opts...)
	if err != nil {
		panic(err)
	}
	return a
}

// New is a shortcut for MustNewAnonymizer. It is intended for static
// configuration where invalid options are programmer errors.
func New(opts ...Option) *Anonymizer {
	return MustNewAnonymizer(opts...)
}

func newAnonymizerFromConfig(cfg config) (*Anonymizer, error) {
	if err := validateUIDRoot(cfg.uidRoot); err != nil {
		return nil, err
	}
	if len(cfg.secret) == 0 {
		cfg.secret = make([]byte, 32)
		if _, err := rand.Read(cfg.secret); err != nil {
			return nil, fmt.Errorf("generate anonymization secret: %w", err)
		}
	}
	return &Anonymizer{
		config:   cfg,
		uidRemap: make(map[string]string),
	}, nil
}

func validateUIDRoot(root string) error {
	root = strings.TrimSuffix(root, ".")
	if root == "" {
		return fmt.Errorf("%w: empty root", ErrInvalidUIDRoot)
	}
	if len(root) > 62 {
		return fmt.Errorf("%w: root is too long", ErrInvalidUIDRoot)
	}
	for _, component := range strings.Split(root, ".") {
		if component == "" {
			return fmt.Errorf("%w: empty component", ErrInvalidUIDRoot)
		}
		for _, r := range component {
			if r < '0' || r > '9' {
				return fmt.Errorf("%w: %q is not numeric", ErrInvalidUIDRoot, component)
			}
		}
	}
	return nil
}

// ParseTag parses tags formatted as "gggg,eeee", "(gggg,eeee)", or
// "ggggeeee".
func ParseTag(spec string) (Tag, error) {
	original := spec
	spec = strings.TrimSpace(spec)
	spec = strings.TrimPrefix(spec, "(")
	spec = strings.TrimSuffix(spec, ")")
	spec = strings.ReplaceAll(spec, " ", "")
	spec = strings.ReplaceAll(spec, ",", "")
	if len(spec) != 8 {
		return Tag{}, fmt.Errorf("invalid tag %q", original)
	}
	value, err := strconv.ParseUint(spec, 16, 32)
	if err != nil {
		return Tag{}, fmt.Errorf("invalid tag %q: %w", original, err)
	}
	return Tag{Group: uint16(value >> 16), Element: uint16(value)}, nil
}

// RemapUID returns a stable, irreversible replacement for a UID. Repeated
// calls with the same UID return the same replacement for the lifetime of the
// Anonymizer.
func (a *Anonymizer) RemapUID(uid string) string {
	uid = strings.Trim(uid, "\x00 ")
	if uid == "" {
		return ""
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if replacement, ok := a.uidRemap[uid]; ok {
		return replacement
	}

	mac := hmac.New(sha256.New, a.config.secret)
	_, _ = mac.Write([]byte("uid\x00"))
	_, _ = mac.Write([]byte(uid))
	sum := mac.Sum(nil)
	number := new(big.Int).SetBytes(sum[:16]).String()

	replacement := a.config.uidRoot + "." + number
	if len(replacement) > 64 {
		replacement = replacement[:64]
	}
	a.uidRemap[uid] = replacement
	return replacement
}

// Anonymize processes a DICOM Part 10 file or a raw DICOM dataset.
func (a *Anonymizer) Anonymize(data []byte) ([]byte, error) {
	f, err := parseDICOM(data)
	if err != nil {
		return nil, err
	}

	if a.config.rejectBurnedIn && burnedInAnnotations(f.data) {
		return nil, fmt.Errorf("%w: redact the pixels or use WithRejectBurnedInAnnotation(false)", ErrBurnedInAnnotation)
	}

	ctx := &processContext{
		dateOffset: a.dateOffsetForDataset(f.data),
	}
	if err := a.processDataset(f.data, ctx, 0); err != nil {
		return nil, err
	}
	a.ensureDeidentificationMarkers(f.data)

	if f.meta != nil {
		if err := a.processDataset(f.meta, ctx, 0); err != nil {
			return nil, err
		}
	}
	if f.part10 && !a.config.preservePreamble {
		f.zeroPreamble = true
	}
	return encodeDICOM(f)
}

// AnonymizeDICOM is an alias for Anonymize.
func (a *Anonymizer) AnonymizeDICOM(data []byte) ([]byte, error) {
	return a.Anonymize(data)
}

// AnonymizeDataset is an alias for Anonymize.
func (a *Anonymizer) AnonymizeDataset(data []byte) ([]byte, error) {
	return a.Anonymize(data)
}

// AnonymizeBytes is an alias for Anonymize.
func (a *Anonymizer) AnonymizeBytes(data []byte) ([]byte, error) {
	return a.Anonymize(data)
}

// AnonymizeReader reads one DICOM object and returns its anonymized bytes.
func (a *Anonymizer) AnonymizeReader(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return a.Anonymize(data)
}

// AnonymizeStream reads one DICOM object from r and writes the anonymized
// object to w.
func (a *Anonymizer) AnonymizeStream(r io.Reader, w io.Writer) error {
	result, err := a.AnonymizeReader(r)
	if err != nil {
		return err
	}
	n, err := w.Write(result)
	if err != nil {
		return err
	}
	if n != len(result) {
		return io.ErrShortWrite
	}
	return nil
}

// AnonymizeFile reads src, anonymizes it, and writes dst with mode 0600.
func (a *Anonymizer) AnonymizeFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	result, err := a.Anonymize(data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, result, 0o600); err != nil {
		return err
	}
	return os.Chmod(dst, 0o600)
}

// Anonymize is a convenience function that creates a one-use Anonymizer.
// Use NewAnonymizer when processing multiple files in one export.
func Anonymize(data []byte, opts ...Option) ([]byte, error) {
	a, err := NewAnonymizer(opts...)
	if err != nil {
		return nil, err
	}
	return a.Anonymize(data)
}

// AnonymizeDICOM is a convenience alias for Anonymize.
func AnonymizeDICOM(data []byte, opts ...Option) ([]byte, error) {
	return Anonymize(data, opts...)
}

// AnonymizeDataset is a convenience alias for Anonymize.
func AnonymizeDataset(data []byte, opts ...Option) ([]byte, error) {
	return Anonymize(data, opts...)
}

// AnonymizeBytes is an alias for Anonymize.
func AnonymizeBytes(data []byte, opts ...Option) ([]byte, error) {
	return Anonymize(data, opts...)
}

// AnonymizeFile is a convenience function that creates a one-use Anonymizer.
func AnonymizeFile(src, dst string, opts ...Option) error {
	a, err := NewAnonymizer(opts...)
	if err != nil {
		return err
	}
	return a.AnonymizeFile(src, dst)
}

type processContext struct {
	dateOffset int
}

func (a *Anonymizer) processDataset(ds *dataset, ctx *processContext, depth int) error {
	if ds == nil {
		return nil
	}
	if depth > maxSequenceDepth {
		return fmt.Errorf("%w: nested dataset exceeds maximum depth", ErrInvalidDICOM)
	}

	out := ds.elements[:0]
	for _, e := range ds.elements {
		if e.tag.Element == 0x0000 && e.tag.Group != 0x0002 {
			continue
		}
		if e.tag.isOverlayOrCurveGroup() {
			continue
		}

		if e.tag.isPrivate() {
			if !a.keepPrivate(e.tag) {
				continue
			}
			if err := a.processPrivateElement(e, ctx, depth); err != nil {
				return err
			}
			out = append(out, e)
			continue
		}

		action := actionForTag(e.tag)
		switch action {
		case actionRemove:
			continue
		case actionEmpty:
			e.value = nil
			e.items = nil
			out = append(out, e)
			continue
		case actionSetPatientIdentityRemoved:
			e.value = textValue("YES")
			e.items = nil
			out = append(out, e)
			continue
		case actionSetDeidentificationMethod:
			e.value = textValue(deidentificationMethod)
			e.items = nil
			out = append(out, e)
			continue
		case actionSetTemporalModified:
			e.value = textValue("MODIFIED")
			e.items = nil
			out = append(out, e)
			continue
		}

		if e.isSequence() {
			for _, item := range e.items {
				if err := a.processDataset(item, ctx, depth+1); err != nil {
					return err
				}
			}
			out = append(out, e)
			continue
		}

		switch e.vr {
		case vrPN:
			e.value = nil
		case vrUI:
			if !safeUIDTags[e.tag] {
				e.value = a.remapUIDValue(e.value)
			}
		case vrDA, vrDT:
			e.value = shiftDateValue(e.value, e.vr, ctx.dateOffset)
		}
		out = append(out, e)
	}
	ds.elements = out
	sort.SliceStable(ds.elements, func(i, j int) bool {
		return ds.elements[i].tag.key() < ds.elements[j].tag.key()
	})
	return nil
}

func (a *Anonymizer) keepPrivate(tag Tag) bool {
	if _, ok := a.config.privateWhitelist[tag]; ok {
		return true
	}
	if tag.isPrivateCreator() {
		_, ok := a.config.privateCreators[tag]
		return ok
	}
	return false
}

func (a *Anonymizer) processPrivateElement(e *element, ctx *processContext, depth int) error {
	if e.isSequence() {
		for _, item := range e.items {
			if err := a.processDataset(item, ctx, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	switch e.vr {
	case vrPN:
		e.value = nil
	case vrUI:
		e.value = a.remapUIDValue(e.value)
	case vrDA, vrDT:
		e.value = shiftDateValue(e.value, e.vr, ctx.dateOffset)
	}
	return nil
}

func (a *Anonymizer) ensureDeidentificationMarkers(ds *dataset) {
	set := func(tag Tag, valueVR vr, value string) {
		e := ds.find(tag)
		if e == nil {
			e = &element{tag: tag, vr: valueVR}
			ds.elements = insertElementSorted(ds.elements, e)
		}
		e.vr = valueVR
		e.value = textValue(value)
		e.items = nil
		e.encapsulated = false
	}
	set(tagPatientIdentityRemoved, vrCS, "YES")
	set(tagDeidentificationMethod, vrLO, deidentificationMethod)
	set(tagTemporalModified, vrCS, "MODIFIED")
}

func insertElementSorted(elements []*element, newElement *element) []*element {
	index := len(elements)
	for i, e := range elements {
		if e.tag.key() > newElement.tag.key() {
			index = i
			break
		}
	}
	elements = append(elements, nil)
	copy(elements[index+1:], elements[index:])
	elements[index] = newElement
	return elements
}

func (a *Anonymizer) remapUIDValue(value []byte) []byte {
	raw := trimUID(value)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\\")
	for i, part := range parts {
		part = strings.Trim(part, "\x00 ")
		if part != "" {
			parts[i] = a.RemapUID(part)
		}
	}
	return textValue(strings.Join(parts, "\\"))
}

func (a *Anonymizer) dateOffsetForDataset(ds *dataset) int {
	key := patientIdentityKey(ds)
	mac := hmac.New(sha256.New, a.config.secret)
	_, _ = mac.Write([]byte("date\x00"))
	_, _ = mac.Write([]byte(key))
	sum := mac.Sum(nil)
	rangeDays := a.config.dateShiftRange
	modulus := int64(2*rangeDays + 1)
	n := int64(binary.BigEndian.Uint64(sum[:8]))%modulus - int64(rangeDays)
	if n == 0 {
		n = 1
	}
	return int(n)
}

func patientIdentityKey(ds *dataset) string {
	keys := []Tag{
		{0x0010, 0x0020}, // PatientID
		{0x0010, 0x0010}, // PatientName
		{0x0010, 0x1000}, // OtherPatientIDs
		{0x0020, 0x000D}, // StudyInstanceUID
		{0x0020, 0x000E}, // SeriesInstanceUID
		{0x0008, 0x0018}, // SOPInstanceUID
	}
	for _, tag := range keys {
		if e := ds.find(tag); e != nil {
			value := trimText(e.value)
			if value != "" {
				return tag.String() + "=" + value
			}
		}
	}
	return "unknown-patient"
}

func shiftDateValue(value []byte, valueVR vr, offset int) []byte {
	raw := trimText(value)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\\")
	for i, part := range parts {
		if part == "" {
			continue
		}
		var shifted string
		if valueVR == vrDA {
			shifted = shiftDateRange(part, offset)
		} else {
			shifted = shiftDateTime(part, offset)
		}
		parts[i] = shifted
	}
	result := strings.Join(parts, "\\")
	if result == "" {
		return nil
	}
	return textValue(result)
}

func shiftDateRange(value string, offset int) string {
	parts := strings.Split(value, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		shifted, ok := shiftDAComponent(part, offset)
		if !ok {
			parts[i] = ""
			continue
		}
		parts[i] = shifted
	}
	return strings.Join(parts, "-")
}

func shiftDAComponent(value string, offset int) (string, bool) {
	if len(value) != 8 {
		return "", false
	}
	t, err := time.Parse("20060102", value)
	if err != nil {
		return "", false
	}
	return t.AddDate(0, 0, offset).Format("20060102"), true
}

func shiftDateTime(value string, offset int) string {
	digitPrefix := 0
	for digitPrefix < len(value) && digitPrefix < 14 {
		if value[digitPrefix] < '0' || value[digitPrefix] > '9' {
			break
		}
		digitPrefix++
	}
	if digitPrefix < 4 {
		return ""
	}

	dateDigits := digitPrefix
	if dateDigits > 8 {
		dateDigits = 8
	}
	switch dateDigits {
	case 4, 6, 8:
	default:
		return ""
	}

	var layout string
	switch dateDigits {
	case 4:
		layout = "2006"
	case 6:
		layout = "200601"
	case 8:
		layout = "20060102"
	}
	t, err := time.Parse(layout, value[:dateDigits])
	if err != nil {
		return ""
	}
	shiftedDate := t.AddDate(0, 0, offset).Format(layout)
	return shiftedDate + value[dateDigits:]
}

func burnedInAnnotations(ds *dataset) bool {
	if ds == nil {
		return false
	}
	for _, e := range ds.elements {
		if e.tag == tagBurnedInAnnotation && strings.EqualFold(trimText(e.value), "YES") {
			return true
		}
		for _, item := range e.items {
			if burnedInAnnotations(item) {
				return true
			}
		}
	}
	return false
}
