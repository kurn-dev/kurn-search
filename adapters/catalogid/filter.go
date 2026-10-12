package catalogid

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
	if request.partition {
		partition, err := adapter.NewExactFilterTerm("partition_id", request.PartitionID)
		if err != nil {
			return adapter.ExactFilterPlan{}, invalidInput()
		}
		spec.Caller = []adapter.ExactFilterTerm{partition}
	}
	plan, err := adapter.NewExactFilterPlan(spec)
	if err != nil {
		return adapter.ExactFilterPlan{}, invalidInput()
	}
	return plan, nil
}

func (Semantics) IsIncomplete(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Category == CategoryIncomplete
}
