# Build a static gpie binary and drop it into a scratch stage so it can be
# COPY'd into any official PHP image.
FROM golang:1.26-alpine AS build
WORKDIR /src
# Resolve dependencies in their own layer so editing a source file does not
# invalidate the module download.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -buildvcs=false -ldflags="-s -w" -o gpie .

FROM scratch
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
