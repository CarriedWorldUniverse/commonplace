# commonplace k3s deploy (cwb namespace)

## Prerequisites

- `cwb` namespace exists (created by herald's `00-namespace.yaml`).
- **cert-manager** is installed in the cluster (`kubectl apply -f https://github.com/cert-manager/cert-manager/releases/latest/download/cert-manager.yaml` or via helm). The `05-certs.yaml` manifest creates the shared internal CA and both the server cert (`commonplace-tls`) and the interchange client cert (`interchange-client-tls`).

## Apply order

```
# 1. Cert-manager CRs first — the CA and leaf certs must exist before the
#    deployment can mount the TLS secret.
kubectl apply -f deploy/k3s/05-certs.yaml

# 2. Then the rest (namespace, PVC, deployment, service).
kubectl apply -f deploy/k3s/
```

Wait for the certificate to be issued before rolling the deployment:

```sh
kubectl -n cwb wait --for=condition=Ready certificate/commonplace-tls --timeout=120s
```

## Build + import image

```sh
podman build -f cmd/commonplace/Containerfile -t commonplace:dev .
podman save commonplace:dev | sudo k3s ctr images import -
kubectl -n cwb rollout restart deploy/commonplace
```

## gRPC + mTLS

commonplace now serves **gRPC + mTLS** on `:8101` (env `COMMONPLACE_GRPC_ADDR`).
The old `COMMONPLACE_ADDR` HTTP var is gone. Probes use `tcpSocket` (an mTLS
server rejects credential-less HTTP/gRPC health checks).

For local non-cluster development without mTLS, set `COMMONPLACE_DEV_INSECURE=1`.

## Gateway route (interchange side, not in this repo)

commonplace is no longer reached by interchange as a plain HTTP reverse-proxy
route. The interchange gateway translates `/knowledge` requests to gRPC via
`INTERCHANGE_COMMONPLACE_GRPC=commonplace.cwb.svc:8101`, using the
`interchange-client-tls` cert for mTLS. No `/knowledge` entry in
`INTERCHANGE_ROUTES`.

## Embedding model

Requires a reachable ollama serving `nomic-embed-text` (dim 768) at
`COMMONPLACE_EMBED_URL`. The live value points at the dMon host bridge IP
(`192.168.143.133:11434`), not an in-cluster service name.
Changing the model is a one-way door (full re-embed required).
