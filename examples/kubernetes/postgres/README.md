# Postgres-backed shared cache (two builders, no S3)

Two privileged `buildkitd` instances (**builder-a**, **builder-b**) share solver cache **metadata** in a single [CloudNativePG](https://cloudnative-pg.io/) Postgres cluster. Layer blobs stay on each builder's local disk (`emptyDir`).

Requires a BuildKit image built from this repo (postgres cache backend is not in upstream `moby/buildkit` yet).

## Quick start

From this directory:

```bash
chmod +x install-cnpg.sh deploy.sh
./deploy.sh
```

`deploy.sh` will:

1. Install the CNPG operator if `clusters.postgresql.cnpg.io` is not already present
2. Create namespace `buildkit`, Postgres, and both builders
3. `docker build` the daemon image and load it into **kind** / **minikube** when detected

Skip steps with env vars:

```bash
SKIP_CNPG=1 ./deploy.sh          # operator already installed
SKIP_IMAGE=1 ./deploy.sh         # image already in cluster nodes
BUILDKIT_IMAGE=myreg/buildkit:tag ./deploy.sh
```

## Manual steps

### 1. Install CloudNativePG operator

Only needed once per cluster:

```bash
./install-cnpg.sh
```

Or pin a version:

```bash
CNPG_VERSION=1.25.0 ./install-cnpg.sh
```

### 2. Build and load the BuildKit image

```bash
docker build -t buildkit:postgres ../../../
kind load docker-image buildkit:postgres   # if using kind
```

### 3. Apply manifests

```bash
kubectl apply -f 00-namespace.yaml
kubectl apply -f 01-cnpg-secret.yaml
kubectl apply -f 02-cnpg-cluster.yaml
kubectl -n buildkit wait --for=condition=Ready cluster/buildkit-db --timeout=600s
kubectl apply -f 03-buildkit-config.yaml
kubectl apply -f 04-builder-a.yaml
kubectl apply -f 05-builder-b.yaml
```

### 4. Verify

```bash
kubectl -n buildkit get pods
kubectl -n buildkit logs deploy/builder-a | grep -i postgres
kubectl -n buildkit logs deploy/builder-b | grep -i postgres
```

Both should log: `using postgres cache storage backend for global shared cache metadata`.

Probes use `buildctl` against the default unix socket (`unix:///run/buildkit/buildkitd.sock`).
The config must expose that socket alongside TCP; liveness uses a TCP check only so a slow
`buildctl` during load does not restart the pod.

```bash
kubectl -n buildkit port-forward svc/builder-a 1234:1234
export BUILDKIT_HOST=tcp://127.0.0.1:1234
buildctl debug workers
```

## What is shared vs local

| Shared (Postgres) | Per builder |
|-------------------|-------------|
| Solver cache graph / metadata | Blob content under `/var/lib/buildkit` |

Cross-daemon blob rehydration (`SharedCacheResultStorage`) requires **both** postgres and `[cache.s3]` in config. This example omits S3 on purpose.

## Cleanup

```bash
kubectl delete namespace buildkit
# Operator (optional): kubectl delete -f https://raw.githubusercontent.com/.../cnpg-1.25.0.yaml
```

## Files

| File | Purpose |
|------|---------|
| `install-cnpg.sh` | Install CNPG operator into `cnpg-system` |
| `deploy.sh` | End-to-end install + apply |
| `00-namespace.yaml` | `buildkit` namespace |
| `01-cnpg-secret.yaml` | DB user/password (dev defaults) |
| `02-cnpg-cluster.yaml` | CNPG `Cluster` (service `buildkit-db-rw`) |
| `03-buildkit-config.yaml` | `buildkitd.toml` with `[cache] backend = "postgres"` |
| `04-builder-a.yaml` | Deployment + Service |
| `05-builder-b.yaml` | Deployment + Service |
