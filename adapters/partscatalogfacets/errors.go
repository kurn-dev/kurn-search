package partscatalogfacets

import "fmt"

// Category is the adapter-local refusal vocabulary. Errors that must keep the
// delivered discover classification are delegated, not reclassified here.
type Category string

const (
	// CategoryInvalidInput marks a caller request that fails the closed
	// grammar; in practice unreachable because the delegated compiler refuses
	// the same bytes first, but kept fail-closed for direct use.
	CategoryInvalidInput Category = "invalid_input"
	// CategoryInvalidSummary marks validated delegate output that cannot
	// produce a lawful summary; an internal inconsistency, never caller input.
	CategoryInvalidSummary Category = "invalid_summary"
)

// Error is the adapter-local error shape for the facet seams only.
type Error struct{ Category Category }

func (e *Error) Error() string {
	if e == nil {
		return "partscatalogfacets: invalid_summary"
	}
	return fmt.Sprintf("partscatalogfacets: %s", e.Category)
}

func invalidInput() *Error   { return &Error{Category: CategoryInvalidInput} }
func invalidSummary() *Error { return &Error{Category: CategoryInvalidSummary} }
