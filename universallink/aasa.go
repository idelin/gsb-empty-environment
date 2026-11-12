package universallink

// Association is the subset of an apple-app-site-association document that
// describes universal links.
type Association struct {
	AppLinks AppLinks `json:"applinks"`
}

// AppLinks is the "applinks" object of the association document.
type AppLinks struct {
	Details []Detail `json:"details"`
}

// Detail binds a set of path components to one or more application IDs.
type Detail struct {
	AppIDs     []string    `json:"appIDs"`
	Components []Component `json:"components"`
}

// Component is a single universal-link rule. A nil Path or Fragment means the
// corresponding URL part is unconstrained; a nil Query means the query string
// is unconstrained.
type Component struct {
	Path     *string           `json:"/"`
	Query    map[string]string `json:"?"`
	Fragment *string           `json:"#"`
	Exclude  bool              `json:"exclude"`
	Comment  string            `json:"comment"`
}
