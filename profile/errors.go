package profile

import "fmt"

type Category string

const (
	CategoryInvalidProfile   Category = "invalid_profile"
	CategoryIdentityMismatch Category = "identity_mismatch"
)

// Error is a bounded profile refusal. It never reflects supplied names,
// digests, versions, paths, or artifact bytes.
type Error struct {
	Category Category
}

func (e *Error) Error() string {
	if e == nil {
		return "profile: invalid_profile"
	}
	return fmt.Sprintf("profile: %s", e.Category)
}

func invalidProfile() *Error   { return &Error{Category: CategoryInvalidProfile} }
func identityMismatch() *Error { return &Error{Category: CategoryIdentityMismatch} }
