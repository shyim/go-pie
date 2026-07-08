# Build a static rpie binary and drop it into a scratch stage so it can be
# COPY'd into any official PHP image.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o rpie .

FROM scratch
COPY --from=build /src/rpie /rpie
ENTRYPOINT ["/rpie"]
