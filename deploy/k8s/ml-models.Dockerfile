# The inference image plus the trained model artifacts, for Kubernetes
# (Compose mounts ml/models instead). Build context: ml/models.
#   docker build -t nexus-city/ml-models:dev -f deploy/k8s/ml-models.Dockerfile ml/models
FROM nexus-city/ml:dev
COPY --chown=root:root . /models
