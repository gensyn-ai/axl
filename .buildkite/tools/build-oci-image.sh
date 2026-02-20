#!/bin/bash
set -euo pipefail

gcloud auth print-access-token --impersonate-service-account=buildkite-agent@gensyn-ci-live-25e0.iam.gserviceaccount.com --lifetime=6h | podman login -u oauth2accesstoken --password-stdin europe-docker.pkg.dev


# Use the Daemon-ful Buildkit Image to run our builds.
# Provide params to `build-oci-image` that Buildkit expects.
# Documentation here https://github.com/moby/buildkit
podman run \
    --rm \
    --network build \
    --privileged \
    -v "$(pwd):/workdir" \
    -v /run/containers/0/auth.json:/root/.docker/config.json:ro,z \
    -e DOCKER_CONFIG=/root/.docker \
    --entrypoint buildctl-daemonless.sh \
    docker.io/moby/buildkit:v0.16.0 \
        build \
        --frontend dockerfile.v0 \
        "$@"

