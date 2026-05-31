# commonplace k3s deploy (cwb namespace)

Mirrors herald/ledger. Build + import the image, then apply:

```
podman build -f cmd/commonplace/Containerfile -t commonplace:dev .
podman save commonplace:dev | sudo k3s ctr images import -
kubectl apply -f deploy/k3s/
```

## Gateway route (interchange side, not in this repo)

commonplace is fronted by interchange-gateway. Add to the gateway's
Routes config:

```
"/knowledge": "http://commonplace.cwb.svc.cluster.local:8101"
```

The gateway verifies the herald token and injects X-CWB-* before
proxying (stripping the `/knowledge` prefix). commonplace trusts those
headers (plan D6) and is ClusterIP-locked — never expose it directly.

## Embedding model

Requires a reachable ollama serving `nomic-embed-text` (dim 768) at
`COMMONPLACE_EMBED_URL`. This is the one external runtime dependency.
Patch the env value if ollama lives outside the cwb namespace.
Changing the model is a one-way door (full re-embed required).
