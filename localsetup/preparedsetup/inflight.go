package preparedsetup

import (
	"github.com/kurn-dev/kurn-search/adapters/partscatalogsuggest"
	"github.com/kurn-dev/kurn-search/internal/localproof"
	"github.com/kurn-dev/kurn-search/profile"
)

const (
	// defaultMaxInflight is the admission limit bound into a version prepared
	// without -max-inflight. Four concurrent requests per searchd measured
	// about 2.5x the single-request throughput at sub-millisecond p99 on the
	// W9 test host.
	defaultMaxInflight = 4
	maxMaxInflight     = 64
	// defaultQueueFactor × max_inflight is the queue bound written into a
	// version prepared without -max-queue; maxMaxQueue is the service's
	// ceiling (searchservice.MaxServiceQueue).
	defaultQueueFactor = 4
	maxMaxQueue        = 4096
)

func validMaxInflight(n int) bool { return n >= 1 && n <= maxMaxInflight }
func validMaxQueue(n int) bool    { return n >= 1 && n <= maxMaxQueue }

// parseMaxQueue accepts a plain decimal 1..4096 (no sign or leading zero).
func parseMaxQueue(raw string) (int, bool) {
	if raw == "" || len(raw) > 4 || raw[0] == '0' {
		return 0, false
	}
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, validMaxQueue(n)
}

// validPolicy mirrors the adapter's policy vocabulary: the default or the
// edit-verifier experiment.
func validPolicy(policy string) bool {
	return policy == "" || policy == partscatalogsuggest.PolicyOSA1 || policy == partscatalogsuggest.PolicyOSA1G60 || policy == partscatalogsuggest.PolicyOSA1G60Union
}

// parseMaxInflight accepts a plain decimal 1..64 (no sign or leading zero).
func parseMaxInflight(raw string) (int, bool) {
	if raw == "" || len(raw) > 2 || raw[0] == '0' {
		return 0, false
	}
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, validMaxInflight(n)
}

// descriptorFor is the one parts-catalog-suggest descriptor a prepared
// version binds; its admission limit, policy and tested engine vary. A new
// preparation binds the current engine label; verification rebuilds an
// existing version's descriptor with the label its manifest records, which
// must belong to an admitted engine pair.
func descriptorFor(maxInflight int, policy, engine string) (profile.Descriptor, error) {
	if !localproof.EngineLabelAdmitted(engine) {
		return profile.Descriptor{}, errRefused
	}
	return partscatalogsuggest.NewDescriptor(partscatalogsuggest.OperationalBinding{PhysicalListName: listName, ConfigSHA256: configSHA, Policy: policy,
		TestedEngines: []string{engine}, TimeoutMillis: 30000, MaxInflight: maxInflight, WorstCaseChargeBytes: 1073741824})
}
