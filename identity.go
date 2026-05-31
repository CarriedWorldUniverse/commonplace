package commonplace

import (
	"net/http"
	"strings"
)

// Identity is the gateway-verified caller (plan D6). Read from the
// mTLS-injected X-CWB-* headers; never re-verified here.
type Identity struct {
	Subject string
	Org     string
	Kind    string
	Scopes  []string
}

// identityFromRequest reads the trusted X-CWB-* headers. The gateway
// strips any client-supplied copies before injecting verified values,
// so these are trustworthy on the ClusterIP hop.
func identityFromRequest(r *http.Request) Identity {
	return Identity{
		Subject: r.Header.Get("X-CWB-Subject"),
		Org:     r.Header.Get("X-CWB-Org"),
		Kind:    r.Header.Get("X-CWB-Kind"),
		Scopes:  strings.Fields(r.Header.Get("X-CWB-Scopes")),
	}
}

func (id Identity) hasScope(want string) bool {
	for _, s := range id.Scopes {
		if s == want {
			return true
		}
	}
	return false
}

const (
	scopeRead  = "knowledge:read"
	scopeWrite = "knowledge:write"
)
