# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS build
WORKDIR /workspace
# Named context is an independently checked-out contracts release.
COPY --from=contracts / /workspace/pulse-contracts
COPY . /workspace/pulse-backend
RUN go work init ./pulse-backend ./pulse-contracts && \
    go work edit -replace=github.com/effitech-pulse/pulse-contracts@v0.1.1=./pulse-contracts
WORKDIR /workspace/pulse-backend
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -o /out/adminuser ./cmd/adminuser && \
    CGO_ENABLED=0 go build -trimpath -o /out/cohort ./cmd/cohort
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/api /app/api
COPY --from=build /out/migrate /app/migrate
COPY --from=build /out/adminuser /app/adminuser
COPY --from=build /out/cohort /app/cohort
ENV HTTP_ADDR=:8000
EXPOSE 8000
ENTRYPOINT ["/app/api"]
