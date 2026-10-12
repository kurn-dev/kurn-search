package preparedsetup

import "bytes"

// Reproject re-runs prepare-artifact's source validation and projection on a
// retained source file and returns the exact projected NDJSON bytes and their
// SHA-256 that prepare would write as projected.ndjson, plus the key count.
// It lets a later consumer prove that a build's retained projected lines are
// the projection of that source, without trusting the manifest's claims.
func Reproject(sourcePath string) ([]byte, string, int, error) {
	set, err := readSource(sourcePath)
	if err != nil {
		return nil, "", 0, err
	}
	return serializeProjected(set)
}

// Mapping returns a copy of the fixed mapping bytes prepare-artifact writes as
// mapping.json for the parts-catalog-suggest profile.
func Mapping() []byte { return bytes.Clone(mappingBytes) }
