package preparedsetup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

func serializeProjected(source sourceSet) ([]byte, string, int, error) {
	var out bytes.Buffer
	for _, record := range source.records {
		if len(record.line) < 2 || record.line[len(record.line)-1] != '\n' || bytes.IndexByte(record.line[:len(record.line)-1], '\n') >= 0 {
			return nil, "", 0, errRefused
		}
		out.Write(record.line)
		var got sourceRecord
		if strictDecode(record.line[:len(record.line)-1], &got) != nil || got != record.record {
			return nil, "", 0, errRefused
		}
	}
	sum := sha256.Sum256(out.Bytes())
	return out.Bytes(), hex.EncodeToString(sum[:]), len(source.records), nil
}
