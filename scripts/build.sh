#!/usr/bin/env bash
set -euo pipefail

VERSION="${VERSION:-0.1.0}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
DATE="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"

PLATFORMS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
)

DIST_DIR="dist"
mkdir -p "${DIST_DIR}"

LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}"

echo "Starting cross-compilation for helm-unwedge (version: ${VERSION}, commit: ${COMMIT})..."

for PLATFORM in "${PLATFORMS[@]}"; do
    GOOS="${PLATFORM%/*}"
    GOARCH="${PLATFORM#*/}"
    OUTPUT_NAME="helm-unwedge-${GOOS}-${GOARCH}"
    if [ "${GOOS}" = "windows" ]; then
        OUTPUT_NAME="${OUTPUT_NAME}.exe"
    fi

    echo "Building ${OUTPUT_NAME}..."
    CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" go build \
        -trimpath \
        -ldflags="${LDFLAGS}" \
        -o "${DIST_DIR}/${OUTPUT_NAME}" \
        ./cmd/helm-unwedge

    # Generate compressed release asset
    if [ "${GOOS}" = "windows" ]; then
        (cd "${DIST_DIR}" && tar -czf "${OUTPUT_NAME}.tar.gz" "${OUTPUT_NAME}")
    else
        (cd "${DIST_DIR}" && tar -czf "${OUTPUT_NAME}.tar.gz" "${OUTPUT_NAME}")
    fi
done

echo "Calculating SHA256 checksums..."
(cd "${DIST_DIR}" && sha256sum * > checksums.txt 2>/dev/null || shasum -a 256 * > checksums.txt)

echo "Cross-compilation complete. Artifacts located in ${DIST_DIR}/"
