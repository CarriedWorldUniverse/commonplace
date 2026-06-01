package commonplace

// Identity is the gateway-verified caller. Read from the cwb-* gRPC metadata
// keys injected by interchange; never re-verified here.
type Identity struct {
	Subject string
	Org     string
	Kind    string
	Scopes  []string
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
