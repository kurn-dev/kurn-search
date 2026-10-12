package preparedsetup

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
	"github.com/kurn-dev/kurn-search/internal/localproof"
)

type readinessBinding struct{ tenant, partition, query, expectedID string }

func preflight(source sourceSet, tenant, partition string) (readinessBinding, error) {
	if tenant == "" || partition == "" {
		return readinessBinding{}, errRefused
	}
	var selected *sourceRecord
	for i := range source.records {
		r := &source.records[i].record
		if r.TenantID == tenant && r.PartitionID == partition {
			if selected != nil {
				return readinessBinding{}, errRefused
			}
			selected = r
		}
	}
	if selected == nil || utf8.RuneCountInString(selected.Identifier) > 256 {
		return readinessBinding{}, errRefused
	}
	request, _ := json.Marshal(map[string]any{"limit": 1, "partition_id": partition, "query": selected.Identifier})
	scope, _ := json.Marshal(map[string]string{"tenant_id": tenant})
	sem := partscatalogsuggest.New()
	if _, err := sem.Compile(request, scope); err != nil {
		return readinessBinding{}, errRefused
	}
	if _, err := sem.CompileMembershipFilter(request, scope); err != nil {
		return readinessBinding{}, errRefused
	}
	if localproof.KurnCompatibilityLabel == "" {
		return readinessBinding{}, errRefused
	}
	return readinessBinding{tenant: tenant, partition: partition, query: selected.Identifier, expectedID: selected.ID}, nil
}
