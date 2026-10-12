package adapter

import "fmt"

// Category is a stable bounded refusal category. It never contains caller or
// artifact material.
type Category string

const (
	CategoryInvalidContract  Category = "invalid_contract"
	CategoryIdentityMismatch Category = "identity_mismatch"
	CategoryIncomplete       Category = "incomplete"
)

// Error is the reusable pure-contract error shape.
type Error struct {
	Category Category
}

func (e *Error) Error() string {
	if e == nil {
		return "adapter: invalid_contract"
	}
	return fmt.Sprintf("adapter: %s", e.Category)
}

func invalidContract() *Error  { return &Error{Category: CategoryInvalidContract} }
func identityMismatch() *Error { return &Error{Category: CategoryIdentityMismatch} }
func incomplete() *Error       { return &Error{Category: CategoryIncomplete} }
