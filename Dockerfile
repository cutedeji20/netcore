# syntax=docker/dockerfile:1
# §72 container security: non-root, minimal, no secrets, read-only rootfs.

FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates tzdata
COPY go.mod ./
COPY . .
# CGO off + trimpath so the binary is static and reproducible.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bootstrap ./cmd/bootstrap

FROM scratch
# tzdata is required: §21A.7 computes quota reset boundaries in the tenant's
# timezone. Without it, LoadLocation("Africa/Lagos") fails and every period
# silently falls back to UTC — an hour of quota attributed to the wrong day.
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/api /api
COPY --from=build /out/worker /worker
COPY --from=build /out/bootstrap /bootstrap

# §72: non-root. scratch has no /etc/passwd, so use a numeric UID.
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/api"]
