package commonplace

import (
	"context"
	"net/http"
	"strings"
)

// Identity is the gateway-verified caller (plan D6). Read from the
// mTLS-injected X-CWB-* headers; never re-verified here. Header-trust is
// sound only because the deploy (Task 7) locks commonplace to a ClusterIP
// reachable solely over the mTLS gateway hop — direct pod access would let
// a caller forge X-CWB-*.
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

type ctxKey string

const identityCtxKey ctxKey = "cwb-identity"

// withIdentity reads the gateway-injected X-CWB-* headers, rejects requests
// with no subject/org (the gateway always sets both for authed requests;
// their absence means the request didn't transit the gateway), and stashes
// the verified Identity in context for handlers. Plan D6: trust, don't
// re-verify — the ClusterIP-locked deploy is what makes this safe.
func withIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := identityFromRequest(r)
		if id.Subject == "" || id.Org == "" {
			http.Error(w, `{"error":"missing identity"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), identityCtxKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// identityFromContext returns the Identity injected by withIdentity.
func identityFromContext(ctx context.Context) Identity {
	id, _ := ctx.Value(identityCtxKey).(Identity)
	return id
}
