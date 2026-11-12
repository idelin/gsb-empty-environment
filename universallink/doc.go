// Package universallink evaluates iOS universal-link rules from an
// apple-app-site-association file.
//
// The package reproduces the semantics of the "applinks.details[].components"
// rules:
//
//   - A detail entry only applies when one of its appIDs shares the team ID
//     (the prefix before the first dot) of the caller's own appID.
//   - Components are evaluated in order and the first matching component
//     decides. A component with "exclude": true blocks the app from opening.
//   - The "/", "?" and "#" fields match the URL path, query string and
//     fragment respectively. Patterns use '*' for any run of characters and
//     '?' for a single character; query item names are literal keys while
//     their values are patterns.
//   - URL paths have their dot segments removed before matching. Encoded
//     slashes (%2F) and duplicate slashes are rejected outright so that
//     crafted paths such as /share/../account cannot bypass the rules.
package universallink
