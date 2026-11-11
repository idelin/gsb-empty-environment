package hartransport

// HAR 1.2 data model. Only the fields needed to faithfully round-trip
// HTTP requests and responses are modelled; unused optional sections are
// still emitted so the output validates as a HAR 1.2 document.
// See http://www.softwareishard.com/blog/har-12-spec/

// HAR is the root object of a HAR 1.2 document.
type HAR struct {
	Log Log `json:"log"`
}

// Log holds the recorded entries.
type Log struct {
	Version string   `json:"version"`
	Creator Creator  `json:"creator"`
	Browser *Browser `json:"browser,omitempty"`
	Pages   []Page   `json:"pages"`
	Entries []Entry  `json:"entries"`
}

// Creator describes the tool that produced the HAR.
type Creator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Browser is an optional HAR section.
type Browser struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Page is an optional grouping object; recordings do not use pages.
type Page struct{}

// Entry pairs a recorded request with its response.
type Entry struct {
	Pageref         string   `json:"pageref,omitempty"`
	StartedDateTime string   `json:"startedDateTime"`
	Time            float64  `json:"time"`
	Request         Request  `json:"request"`
	Response        Response `json:"response"`
	Cache           Cache    `json:"cache"`
	Timings         Timings  `json:"timings"`
	ServerIPAddress string   `json:"serverIPAddress,omitempty"`
	Connection      string   `json:"connection,omitempty"`
}

// Request is the recorded request side of an entry.
type Request struct {
	Method      string    `json:"method"`
	URL         string    `json:"url"`
	HTTPVersion string    `json:"httpVersion"`
	Cookies     []Cookie  `json:"cookies"`
	Headers     []Header  `json:"headers"`
	QueryString []Query   `json:"queryString"`
	PostData    *PostData `json:"postData,omitempty"`
	HeadersSize int       `json:"headersSize"`
	BodySize    int       `json:"bodySize"`
}

// Response is the recorded response side of an entry.
type Response struct {
	Status      int      `json:"status"`
	StatusText  string   `json:"statusText"`
	HTTPVersion string   `json:"httpVersion"`
	Cookies     []Cookie `json:"cookies"`
	Headers     []Header `json:"headers"`
	Content     Content  `json:"content"`
	RedirectURL string   `json:"redirectURL"`
	HeadersSize int      `json:"headersSize"`
	BodySize    int      `json:"bodySize"`
}

// Header is a single HTTP header field.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Query is a single URL query parameter.
type Query struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Cookie is a single cookie parsed from Cookie/Set-Cookie headers.
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Path     string `json:"path,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Expires  string `json:"expires,omitempty"`
	HTTPOnly bool   `json:"httpOnly,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
}

// PostData carries a recorded request body.
type PostData struct {
	MimeType string      `json:"mimeType"`
	Text     string      `json:"text"`
	Params   []PostParam `json:"params,omitempty"`
	Comment  string      `json:"comment,omitempty"`
}

// PostParam is used for form-encoded bodies; recordings keep the raw body
// in Text and leave this empty.
type PostParam struct {
	Name        string `json:"name"`
	Value       string `json:"value,omitempty"`
	FileName    string `json:"fileName,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

// Content carries a recorded response body.
type Content struct {
	Size        int    `json:"size"`
	Compression int    `json:"compression,omitempty"`
	MimeType    string `json:"mimeType"`
	Text        string `json:"text,omitempty"`
	Encoding    string `json:"encoding,omitempty"`
}

// Cache is a required HAR section that recordings do not populate.
type Cache struct{}

// Timings describes request phase timings. Recordings do not measure them;
// zero values are legal per the HAR 1.2 spec.
type Timings struct {
	Blocked float64 `json:"blocked"`
	DNS     float64 `json:"dns"`
	Connect float64 `json:"connect"`
	Send    float64 `json:"send"`
	Wait    float64 `json:"wait"`
	Receive float64 `json:"receive"`
	Ssl     float64 `json:"ssl"`
}
