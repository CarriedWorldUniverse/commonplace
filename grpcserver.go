package commonplace

import (
	"context"
	"errors"
	"time"

	cwbv1 "github.com/CarriedWorldUniverse/cwb-proto/gen/go/cwb/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// KnowledgeServer implements cwbv1.KnowledgeServiceServer backed by the
// unchanged Service/store/crud/search layers. Identity is read from gRPC
// metadata (cwb-* keys injected by interchange) instead of HTTP headers.
type KnowledgeServer struct {
	cwbv1.UnimplementedKnowledgeServiceServer
	svc *Service
}

// NewKnowledgeServer wraps svc in the gRPC server implementation.
func NewKnowledgeServer(svc *Service) *KnowledgeServer {
	return &KnowledgeServer{svc: svc}
}

// Store persists a new knowledge entry. Requires knowledge:write scope.
func (k *KnowledgeServer) Store(ctx context.Context, r *cwbv1.StoreRequest) (*cwbv1.StoreResponse, error) {
	id, ok := identityFromMD(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing identity")
	}
	if !id.hasScope(scopeWrite) {
		return nil, status.Error(codes.PermissionDenied, "missing scope knowledge:write")
	}
	e, err := k.svc.Store(ctx, StoreInput{
		Org:        id.Org,
		Owner:      id.Subject,
		Topic:      r.Topic,
		Content:    r.Content,
		Visibility: r.Visibility,
		Tags:       r.Tags,
	})
	if err != nil {
		return nil, toStatus(err)
	}
	return &cwbv1.StoreResponse{Entry: toProtoEntry(e)}, nil
}

// Search runs hybrid retrieval. Requires knowledge:read scope.
func (k *KnowledgeServer) Search(ctx context.Context, r *cwbv1.SearchRequest) (*cwbv1.SearchResponse, error) {
	id, ok := identityFromMD(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing identity")
	}
	if !id.hasScope(scopeRead) {
		return nil, status.Error(codes.PermissionDenied, "missing scope knowledge:read")
	}
	hits, err := k.svc.Search(ctx, SearchInput{
		Org:    id.Org,
		Caller: id.Subject,
		Query:  r.Q,
		TopK:   int(r.TopK),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	pbHits := make([]*cwbv1.Hit, len(hits))
	for i, h := range hits {
		pbHits[i] = &cwbv1.Hit{
			Entry: toProtoEntry(h.Entry),
			Score: h.Score,
		}
	}
	return &cwbv1.SearchResponse{Hits: pbHits}, nil
}

// List returns all entries visible to the caller. Requires knowledge:read scope.
func (k *KnowledgeServer) List(ctx context.Context, _ *cwbv1.ListRequest) (*cwbv1.ListResponse, error) {
	id, ok := identityFromMD(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing identity")
	}
	if !id.hasScope(scopeRead) {
		return nil, status.Error(codes.PermissionDenied, "missing scope knowledge:read")
	}
	entries, err := k.svc.List(ctx, id.Org, id.Subject)
	if err != nil {
		return nil, toStatus(err)
	}
	pbEntries := make([]*cwbv1.Entry, len(entries))
	for i, e := range entries {
		pbEntries[i] = toProtoEntry(e)
	}
	return &cwbv1.ListResponse{Entries: pbEntries}, nil
}

// Get retrieves one entry. Requires knowledge:read scope.
func (k *KnowledgeServer) Get(ctx context.Context, r *cwbv1.GetRequest) (*cwbv1.GetResponse, error) {
	id, ok := identityFromMD(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing identity")
	}
	if !id.hasScope(scopeRead) {
		return nil, status.Error(codes.PermissionDenied, "missing scope knowledge:read")
	}
	e, err := k.svc.Get(ctx, id.Org, id.Subject, r.Id)
	if err != nil {
		return nil, toStatus(err)
	}
	return &cwbv1.GetResponse{Entry: toProtoEntry(e)}, nil
}

// Update mutates an owned entry. Requires knowledge:write scope.
// Proto uses empty-string to signal "not set"; non-empty fields are applied.
func (k *KnowledgeServer) Update(ctx context.Context, r *cwbv1.UpdateRequest) (*cwbv1.UpdateResponse, error) {
	id, ok := identityFromMD(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing identity")
	}
	if !id.hasScope(scopeWrite) {
		return nil, status.Error(codes.PermissionDenied, "missing scope knowledge:write")
	}
	in := UpdateInput{}
	if r.Topic != "" {
		in.Topic = &r.Topic
	}
	if r.Content != "" {
		in.Content = &r.Content
	}
	if r.Visibility != "" {
		in.Visibility = &r.Visibility
	}
	if len(r.Tags) > 0 {
		tags := r.Tags
		in.Tags = &tags
	}
	e, err := k.svc.Update(ctx, id.Org, id.Subject, r.Id, in)
	if err != nil {
		return nil, toStatus(err)
	}
	return &cwbv1.UpdateResponse{Entry: toProtoEntry(e)}, nil
}

// Delete removes an owned entry. Requires knowledge:write scope.
func (k *KnowledgeServer) Delete(ctx context.Context, r *cwbv1.DeleteRequest) (*cwbv1.DeleteResponse, error) {
	id, ok := identityFromMD(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing identity")
	}
	if !id.hasScope(scopeWrite) {
		return nil, status.Error(codes.PermissionDenied, "missing scope knowledge:write")
	}
	if err := k.svc.Delete(ctx, id.Org, id.Subject, r.Id); err != nil {
		return nil, toStatus(err)
	}
	return &cwbv1.DeleteResponse{}, nil
}

// PurgeOrg removes all entries for the caller's org. Requires org:purge scope.
func (k *KnowledgeServer) PurgeOrg(ctx context.Context, _ *cwbv1.PurgeOrgRequest) (*cwbv1.PurgeOrgResponse, error) {
	id, ok := identityFromMD(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing identity")
	}
	if !id.hasScope("org:purge") {
		return nil, status.Error(codes.PermissionDenied, "missing scope org:purge")
	}
	n, err := k.svc.DeleteByOrg(ctx, id.Org)
	if err != nil {
		return nil, toStatus(err)
	}
	return &cwbv1.PurgeOrgResponse{Purged: id.Org, Entries: int32(n)}, nil
}

// toProtoEntry converts the internal Entry to the proto wire type.
func toProtoEntry(e Entry) *cwbv1.Entry {
	return &cwbv1.Entry{
		Id:         e.ID,
		Org:        e.Org,
		Owner:      e.Owner,
		Topic:      e.Topic,
		Content:    e.Content,
		Visibility: e.Visibility,
		Tags:       e.Tags,
		CreatedAt:  e.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  e.UpdatedAt.Format(time.RFC3339),
	}
}

// toStatus maps the internal error sentinels to gRPC status codes.
//
//	ErrNotFound   → codes.NotFound
//	ErrForbidden  → codes.PermissionDenied
//	validation err (bad input from store.go) → codes.InvalidArgument
//	anything else → codes.Internal
func toStatus(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, "not found")
	case errors.Is(err, ErrForbidden):
		return status.Error(codes.PermissionDenied, "not owner")
	case err != nil && isValidationErr(err):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

// isValidationErr recognises the errors emitted by store/crud that indicate
// bad caller input rather than an internal fault: missing required fields,
// invalid visibility, etc. These all begin with "commonplace: store:" or
// "commonplace: update:" and do NOT wrap ErrNotFound/ErrForbidden.
func isValidationErr(err error) bool {
	if err == nil {
		return false
	}
	// Not-found and forbidden have their own sentinels — everything else from
	// store/update that isn't an embed or DB error is a validation failure.
	// We check for the store/update prefixes used in store.go / crud.go.
	msg := err.Error()
	for _, prefix := range []string{
		"commonplace: store: org and owner required",
		"commonplace: store: topic and content required",
		"commonplace: store: visibility must be",
		"commonplace: update: visibility must be",
		"commonplace: search: org and caller required",
	} {
		if len(msg) >= len(prefix) && msg[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
