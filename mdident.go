package commonplace

import (
	"context"
	"strings"

	"google.golang.org/grpc/metadata"
)

// identityFromMD reads the cwb-* gRPC metadata keys injected by interchange
// and returns the caller Identity. Returns (Identity{}, false) if either
// cwb-subject or cwb-org is absent (the gateway always sets both for authed
// requests; their absence means the request didn't transit the gateway).
func identityFromMD(ctx context.Context) (Identity, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return Identity{}, false
	}
	get := func(k string) string {
		v := md.Get(k)
		if len(v) == 0 {
			return ""
		}
		return v[0]
	}
	id := Identity{
		Subject: get("cwb-subject"),
		Org:     get("cwb-org"),
		Kind:    get("cwb-kind"),
		Scopes:  strings.Fields(get("cwb-scopes")),
	}
	if id.Subject == "" || id.Org == "" {
		return Identity{}, false
	}
	return id, true
}
