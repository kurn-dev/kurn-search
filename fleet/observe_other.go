//go:build !unix

package fleet

// Unsupported platforms refuse the process-bound convergence proof rather
// than weakening its liveness, signal, or process-group invariants.
func observeFleet(Plan) (observedFleet, error) { return observedFleet{}, errRefused }
