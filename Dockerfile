# Build a static gpie binary and drop it into a scratch stage so it can be
# COPY'd into any official PHP image.
# --platform=$BUILDPLATFORM pins the build stage to the runner's own
# architecture so a multi-arch build CROSS-compiles instead of running the Go
# toolchain under QEMU, which is many times slower for no benefit: the binary is
# pure Go (CGO_ENABLED=0), so GOARCH alone selects the target.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
# Resolve dependencies in their own layer so editing a source file does not
# invalidate the module download.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Provided by buildx; TARGETARCH is the arch of the image being produced.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-amd64}" \
    go build -buildvcs=false -trimpath -ldflags="-s -w" -o gpie .

FROM scratch
# Static, non-reproducible labels only. The per-build ones (revision, created,
# version) are applied by the publish workflow via docker/metadata-action, so
# they do not invalidate the build cache on every commit.
LABEL org.opencontainers.image.title="gpie" \
      org.opencontainers.image.description="PHP Installer for Extensions (PIE), as a single static binary to COPY into PHP images" \
      org.opencontainers.image.source="https://github.com/shyim/go-pie" \
      org.opencontainers.image.licenses="BSD-3-Clause"
# gpie talks to Packagist, GitHub, and OCI registries over TLS. The intended
# use is `COPY --from=ghcr.io/shyim/gpie /gpie` into a PHP image that already
# has a trust store, but the ENTRYPOINT below also makes this image directly
# runnable — without these roots every HTTPS call would fail to verify.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /src/gpie /gpie
# Numeric UID: scratch has no /etc/passwd to resolve a name against. Ignored
# when the binary is only copied out of this image.
USER 65534:65534
ENTRYPOINT ["/gpie"]
