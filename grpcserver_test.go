package commonplace

import (
	"context"
	"net"
	"testing"

	cwbv1 "github.com/CarriedWorldUniverse/cwb-proto/gen/go/cwb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1 << 20

func newTestGRPCServer(t *testing.T) (*grpc.ClientConn, cwbv1.KnowledgeServiceClient) {
	t.Helper()
	svc := newTestService(t) // reuses existing helper; closes on t.Cleanup

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	cwbv1.RegisterKnowledgeServiceServer(srv, NewKnowledgeServer(svc))
	go func() {
		if err := srv.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			t.Logf("grpc serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		srv.GracefulStop()
		lis.Close()
	})

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, cwbv1.NewKnowledgeServiceClient(conn)
}

// mdCtx returns a context with cwb-* metadata for the given org/subject/scopes.
func mdCtx(org, subject string, scopes ...string) context.Context {
	scopeStr := ""
	for i, s := range scopes {
		if i > 0 {
			scopeStr += " "
		}
		scopeStr += s
	}
	md := metadata.Pairs(
		"cwb-org", org,
		"cwb-subject", subject,
		"cwb-kind", "agent",
		"cwb-scopes", scopeStr,
	)
	return metadata.NewOutgoingContext(context.Background(), md)
}

func grpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	return status.Code(err)
}

// ---------- happy-path tests ----------

func TestGRPC_StoreGetRoundTrip(t *testing.T) {
	_, client := newTestGRPCServer(t)
	ctx := mdCtx("acme", "agent:builder", scopeWrite)

	storeResp, err := client.Store(ctx, &cwbv1.StoreRequest{
		Topic:      "gRPC setup",
		Content:    "how to set up gRPC in Go",
		Visibility: "org",
		Tags:       []string{"grpc", "go"},
	})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if storeResp.Entry == nil || storeResp.Entry.Id == "" {
		t.Fatal("Store: expected non-empty Entry.Id")
	}
	if storeResp.Entry.Topic != "gRPC setup" {
		t.Errorf("Entry.Topic = %q, want gRPC setup", storeResp.Entry.Topic)
	}
	if storeResp.Entry.Org != "acme" {
		t.Errorf("Entry.Org = %q, want acme", storeResp.Entry.Org)
	}
	if storeResp.Entry.Owner != "agent:builder" {
		t.Errorf("Entry.Owner = %q, want agent:builder", storeResp.Entry.Owner)
	}

	// Get it back (read scope is enough)
	getCtx := mdCtx("acme", "agent:builder", scopeRead)
	getResp, err := client.Get(getCtx, &cwbv1.GetRequest{Id: storeResp.Entry.Id})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if getResp.Entry.Id != storeResp.Entry.Id {
		t.Errorf("Get returned id %q, want %q", getResp.Entry.Id, storeResp.Entry.Id)
	}
	if getResp.Entry.Content != "how to set up gRPC in Go" {
		t.Errorf("Get Content = %q, want how to set up gRPC in Go", getResp.Entry.Content)
	}
}

func TestGRPC_List(t *testing.T) {
	_, client := newTestGRPCServer(t)
	writeCtx := mdCtx("acme", "agent:x", scopeWrite)
	readCtx := mdCtx("acme", "agent:x", scopeRead)

	if _, err := client.Store(writeCtx, &cwbv1.StoreRequest{Topic: "a", Content: "alpha"}); err != nil {
		t.Fatalf("Store a: %v", err)
	}
	if _, err := client.Store(writeCtx, &cwbv1.StoreRequest{Topic: "b", Content: "beta"}); err != nil {
		t.Fatalf("Store b: %v", err)
	}

	listResp, err := client.List(readCtx, &cwbv1.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listResp.Entries) != 2 {
		t.Errorf("List returned %d entries, want 2", len(listResp.Entries))
	}
}

func TestGRPC_Search_ReturnsStoredHit(t *testing.T) {
	_, client := newTestGRPCServer(t)
	ctx := mdCtx("acme", "agent:searcher", scopeWrite, scopeRead)

	if _, err := client.Store(ctx, &cwbv1.StoreRequest{
		Topic:      "kubernetes deployment",
		Content:    "how to do a rolling update in kubernetes",
		Visibility: "org",
	}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	searchResp, err := client.Search(ctx, &cwbv1.SearchRequest{Q: "kubernetes rolling", TopK: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(searchResp.Hits) == 0 {
		t.Fatal("Search: expected at least one hit")
	}
	if searchResp.Hits[0].Entry == nil {
		t.Fatal("Search: hit[0].Entry is nil")
	}
	if searchResp.Hits[0].Entry.Topic != "kubernetes deployment" {
		t.Errorf("hit[0].Entry.Topic = %q, want kubernetes deployment", searchResp.Hits[0].Entry.Topic)
	}
}

func TestGRPC_Update(t *testing.T) {
	_, client := newTestGRPCServer(t)
	ctx := mdCtx("acme", "agent:writer", scopeWrite, scopeRead)

	storeResp, err := client.Store(ctx, &cwbv1.StoreRequest{Topic: "orig", Content: "original content"})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	id := storeResp.Entry.Id

	updResp, err := client.Update(ctx, &cwbv1.UpdateRequest{Id: id, Topic: "updated"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updResp.Entry.Topic != "updated" {
		t.Errorf("Update: Entry.Topic = %q, want updated", updResp.Entry.Topic)
	}
	// Content unchanged
	if updResp.Entry.Content != "original content" {
		t.Errorf("Update: Entry.Content = %q, want original content", updResp.Entry.Content)
	}
}

func TestGRPC_DeleteThenGetNotFound(t *testing.T) {
	_, client := newTestGRPCServer(t)
	ctx := mdCtx("acme", "agent:deleter", scopeWrite, scopeRead)

	storeResp, err := client.Store(ctx, &cwbv1.StoreRequest{Topic: "todelete", Content: "bye"})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	id := storeResp.Entry.Id

	if _, err := client.Delete(ctx, &cwbv1.DeleteRequest{Id: id}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = client.Get(ctx, &cwbv1.GetRequest{Id: id})
	if grpcCode(err) != codes.NotFound {
		t.Errorf("Get after Delete: code = %v, want NotFound", grpcCode(err))
	}
}

func TestGRPC_PurgeOrg(t *testing.T) {
	_, client := newTestGRPCServer(t)
	writeCtx := mdCtx("purgeme", "agent:x", scopeWrite)
	purgeCtx := mdCtx("purgeme", "agent:x", "org:purge")

	// Store two entries
	for _, topic := range []string{"p1", "p2"} {
		if _, err := client.Store(writeCtx, &cwbv1.StoreRequest{Topic: topic, Content: "c"}); err != nil {
			t.Fatalf("Store %s: %v", topic, err)
		}
	}

	purgeResp, err := client.PurgeOrg(purgeCtx, &cwbv1.PurgeOrgRequest{})
	if err != nil {
		t.Fatalf("PurgeOrg: %v", err)
	}
	if purgeResp.Purged != "purgeme" {
		t.Errorf("PurgeOrg.Purged = %q, want purgeme", purgeResp.Purged)
	}
	if purgeResp.Entries != 2 {
		t.Errorf("PurgeOrg.Entries = %d, want 2", purgeResp.Entries)
	}

	// Idempotent: second call returns 0 entries, no error
	purgeResp2, err := client.PurgeOrg(purgeCtx, &cwbv1.PurgeOrgRequest{})
	if err != nil {
		t.Fatalf("PurgeOrg second call: %v", err)
	}
	if purgeResp2.Entries != 0 {
		t.Errorf("PurgeOrg second call: Entries = %d, want 0", purgeResp2.Entries)
	}
}

// ---------- auth / scope tests ----------

func TestGRPC_StoreRequiresWriteScope(t *testing.T) {
	_, client := newTestGRPCServer(t)
	// Only read scope — Store must be PermissionDenied
	ctx := mdCtx("acme", "agent:reader", scopeRead)
	_, err := client.Store(ctx, &cwbv1.StoreRequest{Topic: "t", Content: "c"})
	if grpcCode(err) != codes.PermissionDenied {
		t.Errorf("Store with read-only scope: code = %v, want PermissionDenied", grpcCode(err))
	}
}

func TestGRPC_MissingMetadataUnauthenticated(t *testing.T) {
	_, client := newTestGRPCServer(t)
	// No metadata at all
	_, err := client.Store(context.Background(), &cwbv1.StoreRequest{Topic: "t", Content: "c"})
	if grpcCode(err) != codes.Unauthenticated {
		t.Errorf("Store with no metadata: code = %v, want Unauthenticated", grpcCode(err))
	}
}

func TestGRPC_PurgeOrgRequiresPurgeScope(t *testing.T) {
	_, client := newTestGRPCServer(t)
	// Has knowledge:write but not org:purge
	ctx := mdCtx("acme", "agent:writer", scopeWrite)
	_, err := client.PurgeOrg(ctx, &cwbv1.PurgeOrgRequest{})
	if grpcCode(err) != codes.PermissionDenied {
		t.Errorf("PurgeOrg without org:purge: code = %v, want PermissionDenied", grpcCode(err))
	}
}

func TestGRPC_Search_EmptyQuery_InvalidArgument(t *testing.T) {
	_, client := newTestGRPCServer(t)
	ctx := mdCtx("acme", "agent:searcher", scopeRead)
	_, err := client.Search(ctx, &cwbv1.SearchRequest{Q: "", TopK: 5})
	if grpcCode(err) != codes.InvalidArgument {
		t.Errorf("Search with empty q: code = %v, want InvalidArgument", grpcCode(err))
	}
}
