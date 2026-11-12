package taskpkg

import "time"

const (
	// DefaultMinFragments is used when Config does not provide a positive K.
	DefaultMinFragments = 4
	// DefaultMinBytes is used when Config does not provide a positive B.
	DefaultMinBytes = 32

	// HexIndexLen is the number of leading hexadecimal digits that form a
	// fragment number in a DNS label.
	HexIndexLen = 4
)

// Window is a half-open time interval [Start, End).
type Window struct {
	Start time.Time
	End   time.Time
}

// Valid reports whether the window has a non-empty time range.
func (w Window) Valid() bool {
	return w.End.After(w.Start)
}

// Contains reports whether t belongs to the half-open window [Start, End).
func (w Window) Contains(t time.Time) bool {
	return w.Valid() && !t.Before(w.Start) && t.Before(w.End)
}

// Config contains the detection thresholds.
//
// K is the minimum number of distinct fragment numbers and B is the minimum
// number of reassembled bytes. MinFragments and MinBytes are accepted as
// aliases for callers that prefer descriptive names.
type Config struct {
	K            int
	B            int
	MinFragments int
	MinBytes     int

	// Window, when set, is used as the default window for queries that do not
	// carry their own window. Start and End provide the same default without a
	// separate Window value.
	Window *Window
	Start  time.Time
	End    time.Time
}

func (c Config) thresholds() (int, int) {
	k := c.K
	if k <= 0 {
		k = c.MinFragments
	}
	if k <= 0 {
		k = DefaultMinFragments
	}

	b := c.B
	if b <= 0 {
		b = c.MinBytes
	}
	if b <= 0 {
		b = DefaultMinBytes
	}
	return k, b
}

func (c Config) withDefaults() Config {
	k, b := c.thresholds()
	c.K, c.B = k, b
	return c
}

// Query is one observed DNS query.
//
// QueryID is the original query identifier and must be preserved in
// detections. Client identifies the probe-visible client (for example an IP
// address). Domain is the complete queried name. RegisteredDomain is optional:
// when absent, the portion after the payload label is used as the grouping
// key.
//
// Label may be set explicitly when the probe has already extracted the
// payload label. Otherwise the label immediately before RegisteredDomain is
// used, or the left-most label when RegisteredDomain is absent.
type Query struct {
	QueryID string
	ID      string

	Client   string
	ClientID string
	ClientIP string

	Domain           string
	QName            string
	Name             string
	RegisteredDomain string
	BaseDomain       string

	Label string

	Timestamp time.Time
	Time      time.Time
	At        time.Time

	WindowStart time.Time
	WindowEnd   time.Time
	Start       time.Time
	End         time.Time
	Window      *Window
}

// Event, Observation, DNSQuery and Record are aliases for Query.
type Event = Query
type Observation = Query
type DNSQuery = Query
type Record = Query
type Packet = Query

// Thresholds and Options are aliases for Config.
type Thresholds = Config
type Options = Config

func (q Query) queryID() string {
	if q.QueryID != "" {
		return q.QueryID
	}
	return q.ID
}

func (q Query) clientID() string {
	if q.Client != "" {
		return q.Client
	}
	if q.ClientID != "" {
		return q.ClientID
	}
	return q.ClientIP
}

func (q Query) domainName() string {
	if q.Domain != "" {
		return q.Domain
	}
	if q.QName != "" {
		return q.QName
	}
	return q.Name
}

func (q Query) registeredDomain() string {
	if q.RegisteredDomain != "" {
		return q.RegisteredDomain
	}
	return q.BaseDomain
}

func (q Query) timestamp() time.Time {
	if !q.Timestamp.IsZero() {
		return q.Timestamp
	}
	if !q.Time.IsZero() {
		return q.Time
	}
	return q.At
}

func (q Query) explicitWindow() (Window, bool) {
	if q.Window != nil {
		return *q.Window, true
	}
	if !q.WindowStart.IsZero() && !q.WindowEnd.IsZero() {
		return Window{Start: q.WindowStart, End: q.WindowEnd}, true
	}
	if !q.Start.IsZero() && !q.End.IsZero() {
		return Window{Start: q.Start, End: q.End}, true
	}
	return Window{}, false
}

// FragmentEvidence describes one unique fragment used by a detection.
type FragmentEvidence struct {
	Index   uint16
	DataHex string
	Hex     string
	Data    []byte
	ByteLen int

	// QueryID is the earliest original query ID for the canonical content of
	// this fragment. QueryIDs contains every original query ID for canonical
	// content, including retransmissions.
	QueryID  string
	QueryIDs []string

	// Duplicates counts retransmissions with the same fragment number and the
	// same canonical content. Such packets never add to the distinct count.
	Duplicates int
}

// Conflict records one fragment number that was observed with content other
// than the canonical content for that number.
type Conflict struct {
	Index   uint16
	DataHex string
	Hex     string
	Data    []byte
	ByteLen int

	QueryID  string
	QueryIDs []string
	Count    int

	CanonicalDataHex string
	CanonicalQueryID string
}

// Alert is a positive detection for one client, registered domain and window.
type Alert struct {
	Client            string
	ClientID          string
	RegisteredDomain  string
	Domain            string
	BaseDomain        string
	Window            Window
	WindowStart       time.Time
	WindowEnd         time.Time
	Start             time.Time
	End               time.Time
	UniqueFragments   int
	DistinctFragments int
	FragmentCount     int
	ChainFragments    int
	ReassembledBytes  int
	ByteCount         int
	FirstIndex        uint16
	LastIndex         uint16
	Indices           []uint16
	Data              []byte
	Reassembled       []byte
	Payload           []byte
	DataHex           string
	Hex               string
	PayloadHex        string
	QueryID           string
	IDs               []string
	QueryIDs          []string
	OriginalQueryIDs  []string
	AllQueryIDs       []string
	Fragments         []FragmentEvidence
	Evidence          []FragmentEvidence
	AllFragments      []FragmentEvidence
	Conflicts         []Conflict
	Duplicates        int
}

// Detection, Finding and Result are aliases for Alert.
type Detection = Alert
type Finding = Alert
type Result = Alert
type Reassembly = Alert

// WindowReport is an inspection result for a window that has not necessarily
// met the alerting thresholds.
type WindowReport struct {
	Client           string
	RegisteredDomain string
	Window           Window
	UniqueFragments  int
	ReassembledBytes int
	Detected         bool
	Alert            *Alert
	Fragments        []FragmentEvidence
	Conflicts        []Conflict
}

// WindowResult is an alias for WindowReport.
type WindowResult = WindowReport
