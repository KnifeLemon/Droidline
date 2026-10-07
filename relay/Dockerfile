# Self-hosted relay:
#   docker run -d -e RELAY_TOKEN=... -p 8443:8443 ghcr.io/knifelemon/droidline-relay
# Put a TLS proxy in front, or add --domain for Let's Encrypt (needs ports 80 and 443).
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY spec ./spec
COPY server ./server
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /droidline-relay ./server/cmd/droidline-relay

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /droidline-relay /droidline-relay
EXPOSE 8443
ENTRYPOINT ["/droidline-relay"]
