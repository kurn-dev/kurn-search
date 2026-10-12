package preparedsetup

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

type credential struct {
	Handle string `json:"handle"`
	Tenant string `json:"tenant"`
}
type credentialsDocument struct {
	Credentials []credential `json:"credentials"`
	Schema      string       `json:"schema"`
}

func randomHex() (string, error) {
	var raw [32]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return "", errRefused
	}
	return hex.EncodeToString(raw[:]), nil
}

func makeSecrets(tenants []string) (backend []byte, kurnd []byte, client []byte, handles map[string]string, err error) {
	key, e := randomHex()
	if e != nil {
		return nil, nil, nil, nil, e
	}
	backend = []byte(key)
	kurnd = []byte(key + "\n")
	handles = map[string]string{}
	doc := credentialsDocument{Schema: "kurn-search-prepared-client-credentials/v1"}
	for _, tenant := range tenants {
		h, x := randomHex()
		if x != nil {
			return nil, nil, nil, nil, x
		}
		handles[tenant] = h
		doc.Credentials = append(doc.Credentials, credential{Handle: h, Tenant: tenant})
	}
	client, e = json.Marshal(doc)
	if e != nil {
		return nil, nil, nil, nil, errRefused
	}
	client = append(client, '\n')
	return backend, kurnd, client, handles, nil
}
