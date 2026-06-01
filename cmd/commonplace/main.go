// Command commonplace is the CWB knowledge pillar gRPC service. It runs
// behind interchange-gateway over mTLS; identity comes from cwb-* gRPC
// metadata injected by the gateway.
//
// Config (env):
//
//	COMMONPLACE_GRPC_ADDR       listen address (default :8101)
//	COMMONPLACE_DB              sqlite path (default /var/lib/cwb/commonplace.db)
//	COMMONPLACE_TLS_CERT        path to server TLS certificate (PEM)
//	COMMONPLACE_TLS_KEY         path to server TLS private key (PEM)
//	COMMONPLACE_TLS_CA          path to client CA certificate (PEM) for mTLS
//	COMMONPLACE_DEV_INSECURE    set to "1" to skip mTLS (local dev only; fatal if unset without certs)
//	COMMONPLACE_EMBED_PROVIDER  embedding provider (default "ollama")
//	COMMONPLACE_EMBED_URL       ollama base URL (default http://localhost:11434)
//	COMMONPLACE_EMBED_MODEL     embedding model (default nomic-embed-text)
//	COMMONPLACE_EMBED_DIM       embedding dim (default 768)
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log"
	"net"
	"os"
	"strconv"

	cwbv1 "github.com/CarriedWorldUniverse/cwb-proto/gen/go/cwb/v1"
	"github.com/CarriedWorldUniverse/commonplace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	addr := env("COMMONPLACE_GRPC_ADDR", ":8101")
	dbPath := env("COMMONPLACE_DB", "/var/lib/cwb/commonplace.db")

	embedder, err := commonplace.NewOllamaEmbedder(commonplace.OllamaConfig{
		URL:   env("COMMONPLACE_EMBED_URL", "http://localhost:11434"),
		Model: env("COMMONPLACE_EMBED_MODEL", "nomic-embed-text"),
		Dim:   envInt("COMMONPLACE_EMBED_DIM", 768),
	})
	if err != nil {
		log.Fatalf("commonplace: embedder: %v", err)
	}

	svc, err := commonplace.New(context.Background(), commonplace.Config{
		DBPath:   dbPath,
		Embedder: embedder,
	})
	if err != nil {
		log.Fatalf("commonplace: %v", err)
	}
	defer svc.Close()

	grpcSrv := grpc.NewServer(serverOptions()...)
	cwbv1.RegisterKnowledgeServiceServer(grpcSrv, commonplace.NewKnowledgeServer(svc))

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcSrv, healthSrv)
	healthSrv.SetServingStatus("cwb.v1.KnowledgeService", grpc_health_v1.HealthCheckResponse_SERVING)

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("commonplace: listen %s: %v", addr, err)
	}
	log.Printf("commonplace gRPC listening on %s (db=%s)", addr, dbPath)
	if err := grpcSrv.Serve(lis); err != nil {
		log.Fatalf("commonplace: serve: %v", err)
	}
}

// serverOptions builds the gRPC server options. When the TLS env vars are
// set the server enforces mTLS (RequireAndVerifyClientCert). Insecure mode
// requires an explicit COMMONPLACE_DEV_INSECURE=1 opt-in; missing certs
// without the opt-in cause a fatal startup error.
func serverOptions() []grpc.ServerOption {
	certFile := os.Getenv("COMMONPLACE_TLS_CERT")
	keyFile := os.Getenv("COMMONPLACE_TLS_KEY")
	caFile := os.Getenv("COMMONPLACE_TLS_CA")
	if certFile == "" || keyFile == "" || caFile == "" {
		if os.Getenv("COMMONPLACE_DEV_INSECURE") == "1" {
			log.Printf("commonplace: COMMONPLACE_DEV_INSECURE=1 — starting WITHOUT mTLS (dev only)")
			return nil
		}
		log.Fatalf("commonplace: mTLS required — set COMMONPLACE_TLS_CERT/_KEY/_CA (or COMMONPLACE_DEV_INSECURE=1 for local dev)")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Fatalf("commonplace: tls: load cert/key: %v", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		log.Fatalf("commonplace: tls: read CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		log.Fatalf("commonplace: tls: no certs parsed from CA file %s", caFile)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	}
	return []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
