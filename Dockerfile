# boson admin panel: SvelteKit UI built with Node, embedded into the Go
# binary, shipped on distroless. Both build stages run on the build host's
# platform (no QEMU); Go cross-compiles for the target.
FROM --platform=$BUILDPLATFORM node:24-alpine AS ui
WORKDIR /ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /ui/build ./ui/build
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/admin ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder --chown=65532:65532 /out/admin .

ARG VERSION=dev
ENV VERSION=$VERSION ENV=production

# 8080: UI + /api (behind the reverse proxy). 8081: /metrics, /healthz (never publish).
EXPOSE 8080 8081
USER 65532:65532
ENTRYPOINT ["./admin"]
CMD ["serve"]
