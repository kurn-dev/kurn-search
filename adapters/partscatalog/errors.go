// Package partscatalog implements identifier search with exact parts metadata filters.
package partscatalog

// Category is a stable refusal category that never reflects input material.
type Category string

const (
	CategoryInvalidInput    Category = "invalid_input"
	CategoryInvalidPlan     Category = "invalid_plan"
	CategoryInvalidEvidence Category = "invalid_evidence"
	CategoryIncomplete      Category = "incomplete"
)

// Error is the stable package refusal shape.
type Error struct{ Category Category }

func (err *Error) Error() string {
	if err == nil {
		return "partscatalog: invalid_input"
	}
	return "partscatalog: " + string(err.Category)
}

func invalidInput() *Error    { return &Error{Category: CategoryInvalidInput} }
func invalidPlan() *Error     { return &Error{Category: CategoryInvalidPlan} }
func invalidEvidence() *Error { return &Error{Category: CategoryInvalidEvidence} }
func incomplete() *Error      { return &Error{Category: CategoryIncomplete} }
