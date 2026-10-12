package preparedsetup

import (
	"bytes"
	"testing"
)

func TestSecretFramingAndRedaction(t *testing.T) {
	b, k, c, h, e := makeSecrets([]string{"alpha", "beta"})
	if e != nil || len(b) != 64 || len(k) != 65 || k[64] != '\n' || bytes.Contains(b, []byte{'\n'}) || len(h) != 2 || bytes.Contains(c, b) {
		t.Fatal("secret contract")
	}
}
