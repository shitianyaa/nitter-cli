// Pagination envelope of the public SDK. Part of the NDJSON data contract:
// JSON keys are frozen (additive-only).
package nitter

// Page is one page of results. NextCursor is the opaque cursor to request
// the following page; empty means there are no more results. RSS feeds have
// no cursor: their pages carry an empty NextCursor and a single Page.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}
