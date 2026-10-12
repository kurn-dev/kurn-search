package partscatalogsuggest

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/kurn-dev/kurn-search/adapter"
)

// parseSelection checks every supplied member before normalization. Raw JSON
// members are decoded individually so lone surrogates cannot be repaired by
// encoding/json into apparently valid replacement characters.
func parseSelection(name string, raw json.RawMessage) ([]string, error) {
	maximum := adapter.MaxMembershipAlternatives
	if name == "status" {
		maximum = 2
	}
	trimmed := bytes.TrimSpace(raw)
	members := []json.RawMessage{raw}
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if json.Unmarshal(trimmed, &members) != nil || len(members) == 0 || len(members) > maximum {
			return nil, invalidInput()
		}
	}
	values := make([]string, len(members))
	for i, member := range members {
		if !boundedString(member, &values[i]) || (name == "status" && values[i] != "active" && values[i] != "discontinued") {
			return nil, invalidInput()
		}
	}
	term, err := adapter.NewMembershipFilterTerm(name, values)
	if err != nil {
		return nil, invalidInput()
	}
	return term.Values(), nil
}

func (Semantics) CompileMembershipFilter(requestBytes, scopeBytes []byte) (adapter.MembershipFilterPlan, error) {
	request, err := parseRequest(requestBytes)
	if err != nil {
		return adapter.MembershipFilterPlan{}, err
	}
	scope, err := parseScope(scopeBytes)
	if err != nil {
		return adapter.MembershipFilterPlan{}, err
	}
	return membershipPlan(request, scope)
}

// membershipPlan builds the membership filter plan from an already parsed
// request and trusted scope, so callers holding both do not parse them again.
func membershipPlan(request searchRequest, scope trustedScope) (adapter.MembershipFilterPlan, error) {
	tenant, err := adapter.NewExactFilterTerm("tenant_id", scope.TenantID)
	if err != nil {
		return adapter.MembershipFilterPlan{}, invalidInput()
	}
	spec := adapter.MembershipFilterPlanSpec{Trusted: []adapter.ExactFilterTerm{tenant}}
	for _, name := range []string{"category", "manufacturer", "partition_id", "status"} {
		values, present := request.filters[name]
		if name == "partition_id" {
			values, present = []string{request.PartitionID}, request.partition
		}
		if !present {
			continue
		}
		term, err := adapter.NewMembershipFilterTerm(name, values)
		if err != nil {
			return adapter.MembershipFilterPlan{}, invalidInput()
		}
		spec.Caller = append(spec.Caller, term)
	}
	result, err := adapter.NewMembershipFilterPlan(spec)
	if err != nil {
		return adapter.MembershipFilterPlan{}, invalidInput()
	}
	return result, nil
}

func (Semantics) IsIncomplete(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Category == CategoryIncomplete
}
