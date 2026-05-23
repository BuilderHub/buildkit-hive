#!/usr/bin/env bash
# Install CNPG (if needed), Postgres, and two BuildKit builders sharing that DB.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

SKIP_CNPG="${SKIP_CNPG:-}"
SKIP_IMAGE="${SKIP_IMAGE:-}"
BUILDKIT_IMAGE="${BUILDKIT_IMAGE:-buildkit:postgres}"

if [[ -z "${SKIP_CNPG}" ]]; then
  "${SCRIPT_DIR}/install-cnpg.sh"
fi

echo "Applying BuildKit + Postgres manifests..."
kubectl apply -f "${SCRIPT_DIR}/00-namespace.yaml"
kubectl apply -f "${SCRIPT_DIR}/01-cnpg-secret.yaml"
kubectl apply -f "${SCRIPT_DIR}/02-cnpg-cluster.yaml"

echo "Waiting for Postgres cluster..."
kubectl -n buildkit wait --for=condition=Ready cluster/buildkit-db --timeout=600s

if [[ -z "${SKIP_IMAGE}" ]]; then
  echo "Building BuildKit image (${BUILDKIT_IMAGE}) from ${REPO_ROOT}..."
  docker build -t "${BUILDKIT_IMAGE}" "${REPO_ROOT}"
  if command -v kind >/dev/null 2>&1 && kind get clusters 2>/dev/null | grep -q .; then
    for cluster in $(kind get clusters); do
      echo "Loading image into kind cluster ${cluster}..."
      kind load docker-image "${BUILDKIT_IMAGE}" --name "${cluster}"
    done
  fi
  if command -v minikube >/dev/null 2>&1 && minikube status -o json >/dev/null 2>&1; then
    echo "Loading image into minikube..."
    minikube image load "${BUILDKIT_IMAGE}"
  fi
fi

if [[ -f "${SCRIPT_DIR}/06-s3-env.secret.yaml" ]]; then
  kubectl apply -f "${SCRIPT_DIR}/06-s3-env.secret.yaml"
else
  echo "Note: create ${SCRIPT_DIR}/06-s3-env.secret.yaml for S3/R2 (see README)."
fi

kubectl apply -f "${SCRIPT_DIR}/03-buildkit-config.yaml"
kubectl apply -f "${SCRIPT_DIR}/04-builder-a.yaml"
kubectl apply -f "${SCRIPT_DIR}/05-builder-b.yaml"

echo "Waiting for builders..."
kubectl -n buildkit rollout status deployment/builder-a --timeout=300s
kubectl -n buildkit rollout status deployment/builder-b --timeout=300s

echo ""
echo "Done. Services:"
kubectl -n buildkit get svc builder-a builder-b
echo ""
echo "Example:"
echo "  kubectl -n buildkit port-forward svc/builder-a 1234:1234"
echo "  export BUILDKIT_HOST=tcp://127.0.0.1:1234"
echo "  buildctl debug workers"
