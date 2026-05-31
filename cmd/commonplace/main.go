// Command commonplace is the CWB knowledge pillar HTTP service. It runs
// behind interchange-gateway on a ClusterIP and trusts the mTLS-injected
// X-CWB-* identity headers (see plan D6).
//
// Config (env):
//
//	COMMONPLACE_ADDR            listen address (default :8101)
//	COMMONPLACE_DB              sqlite path (default /var/lib/cwb/commonplace.db)
//	COMMONPLACE_EMBED_PROVIDER  embedding provider (default "ollama")
//	COMMONPLACE_EMBED_URL       ollama base URL (default http://localhost:11434)
//	COMMONPLACE_EMBED_MODEL     embedding model (default nomic-embed-text)
//	COMMONPLACE_EMBED_DIM       embedding dim (default 768)
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/CarriedWorldUniverse/commonplace"
)

func main() {
	addr := env("COMMONPLACE_ADDR", ":8101")
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

	log.Printf("commonplace listening on %s (db=%s)", addr, dbPath)
	if err := http.ListenAndServe(addr, svc.Handler()); err != nil {
		log.Fatalf("commonplace: %v", err)
	}
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
