package partscatalog

import (
	"errors"
	"github.com/kurn-dev/kurn-search/adapter"
)

func (Semantics) CompileFilter(requestBytes, scopeBytes []byte) (adapter.ExactFilterPlan, error) {
	request, err := parseRequest(requestBytes)
	if err != nil {
		return adapter.ExactFilterPlan{}, err
	}
	scope, err := parseScope(scopeBytes)
	if err != nil {
		return adapter.ExactFilterPlan{}, err
	}
	tenant, err := adapter.NewExactFilterTerm("tenant_id", scope.TenantID)
	if err != nil {
		return adapter.ExactFilterPlan{}, invalidInput()
	}
	spec := adapter.ExactFilterPlanSpec{Trusted: []adapter.ExactFilterTerm{tenant}}
	for _, name := range []string{"category", "manufacturer", "partition_id", "status"} {
		value, present := request.filters[name]
		if name == "partition_id" {
			value, present = request.PartitionID, request.partition
		}
		if !present {
			continue
		}
		term, err := adapter.NewExactFilterTerm(name, value)
		if err != nil {
			return adapter.ExactFilterPlan{}, invalidInput()
		}
		spec.Caller = append(spec.Caller, term)
	}
	result, err := adapter.NewExactFilterPlan(spec)
	if err != nil {
		return adapter.ExactFilterPlan{}, invalidInput()
	}
	return result, nil
}

func (Semantics) IsIncomplete(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Category == CategoryIncomplete
}
