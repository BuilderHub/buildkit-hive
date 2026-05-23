# Postgres + S3 shared cache (two builders)

Two privileged `buildkitd` instances (**builder-a**, **builder-b**) share solver cache **metadata** in [CloudNativePG](https://cloudnative-pg.io/) Postgres and **blobs** in S3-compatible storage (e.g. Cloudflare R2) via env vars in Secret `buildkit-s3-env`.

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

### 3. S3 / R2 credentials (env vars)

```bash
cp 06-s3-env.secret.example.yaml 06-s3-env.secret.yaml
# Edit: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_BUCKET, AWS_ENDPOINT_URL
kubectl apply -f 06-s3-env.secret.yaml
```

Or create the secret without a file:

```bash
kubectl -n buildkit create secret generic buildkit-s3-env \
  --from-literal=AWS_ACCESS_KEY_ID='YOUR_R2_ACCESS_KEY_ID' \
  --from-literal=AWS_SECRET_ACCESS_KEY='YOUR_R2_SECRET_ACCESS_KEY' \
  --from-literal=AWS_BUCKET='buildkit-hive-test' \
  --from-literal=AWS_REGION='auto' \
  --from-literal=AWS_ENDPOINT_URL='https://YOUR_ACCOUNT_ID.r2.cloudflarestorage.com'
```

For **Cloudflare R2**, use the **S3 API** access key pair from the R2 dashboard (not the account API token value). `AWS_REGION=auto` is fine with a custom endpoint.

### 4. Apply manifests

```bash
kubectl apply -f 00-namespace.yaml
kubectl apply -f 01-cnpg-secret.yaml
kubectl apply -f 02-cnpg-cluster.yaml
kubectl -n buildkit wait --for=condition=Ready cluster/buildkit-db --timeout=600s
kubectl apply -f 03-buildkit-config.yaml
kubectl apply -f 04-builder-a.yaml
kubectl apply -f 05-builder-b.yaml
```

### 5. Verify

```bash
kubectl -n buildkit get pods
kubectl -n buildkit logs deploy/builder-a | grep -iE 'postgres|s3|tiered'
kubectl -n buildkit logs deploy/builder-b | grep -iE 'postgres|s3|tiered'
```

Expect postgres cache backend and tiered S3 content store wiring when the secret is present.

Probes use `buildctl` against the default unix socket (`unix:///run/buildkit/buildkitd.sock`).
The config must expose that socket alongside TCP; liveness uses a TCP check only so a slow
`buildctl` during load does not restart the pod.

```bash
kubectl -n buildkit port-forward svc/builder-a 1234:1234
export BUILDKIT_HOST=tcp://127.0.0.1:1234
buildctl debug workers
```

## What is shared vs local

| Shared | Per builder |
|--------|-------------|
| Postgres: solver cache graph / metadata + OCI descriptors | Local hot tier under `/var/lib/buildkit` |
| S3/R2: authoritative blobs (async upload by default) | Same bucket prefix for all builders |

Cross-builder cache hits require **both** postgres and `[cache.s3]`. Layer descriptors are written to Postgres on each cached step; blobs upload to S3 in the background by default.

### Performance tuning (`[cache.s3]`)

| Setting | Default | Effect |
|---------|---------|--------|
| `syncUploadOnSave` | `false` | Faster first build; cross-builder may wait briefly for async upload |
| `syncUploadOnSave` | `true` | Blocks on R2 upload per layer; immediate cross-builder reuse |
| `uploadTopLayerOnly` | `true` | Sync upload only the newest layer (parents assumed already in S3) |
| `prefetchOnLoad` | `true` | Parallel S3→local pull before rehydrating cache on another builder |
| `uploadParallelism` | `4` | Concurrent S3 uploads and prefetch pulls |
| `existsRetryAttempts` | `5` async / `1` sync | How long to wait for blobs to appear in S3 |
| `existsRetryInterval` | `2s` | Delay between Exists retries |

For repeat builds on the **same builder**, use a **persistent volume** on `/var/lib/buildkit` (PVC) so the local hot tier survives pod restarts.

**Note:** Cache entries created before cross-builder descriptor export was enabled will not cross-hit until rebuilt once.

### Env vars (injected via `buildkit-s3-env`)

| Variable | Purpose |
|----------|---------|
| `AWS_ACCESS_KEY_ID` | S3 access key |
| `AWS_SECRET_ACCESS_KEY` | S3 secret key |
| `AWS_BUCKET` | Bucket name |
| `AWS_REGION` | Region (`auto` for R2) |
| `AWS_ENDPOINT_URL` | Custom endpoint (required for R2) |
| `AWS_SESSION_TOKEN` | Optional; usually unset for R2 |

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
| `03-buildkit-config.yaml` | `buildkitd.toml` postgres + `[cache.s3]` |
| `04-builder-a.yaml` | Deployment + Service (`envFrom` secret) |
| `05-builder-b.yaml` | Deployment + Service (`envFrom` secret) |
| `06-s3-env.secret.example.yaml` | Template for S3 env secret (copy → `06-s3-env.secret.yaml`) |
