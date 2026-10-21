ARG BUILDPLATFORM
ARG TARGETPLATFORM

FROM --platform=${BUILDPLATFORM} node:26-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json frontend/tsconfig.json frontend/vite.config.ts frontend/index.html ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY frontend/src ./src
RUN npm run build

FROM --platform=${BUILDPLATFORM} golang:1.27.2 AS build
WORKDIR /src
ARG TARGETOS
ARG TARGETARCH
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags='-s -w' -o /out/mariner ./cmd/mariner
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -mod=mod -tags='no_clickhouse no_mysql no_mssql no_azuresql no_libsql no_vertica no_ydb' -trimpath -ldflags='-s -w' -o /out/goose github.com/pressly/goose/v3/cmd/goose
RUN mkdir -p /out/migrations && cp internal/database/migrations/*.sql /out/migrations/

FROM --platform=${TARGETPLATFORM} gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mariner /mariner
COPY --from=build /out/goose /usr/local/bin/goose
COPY --from=build /out/migrations /migrations
COPY --from=frontend /src/frontend/dist /web
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/mariner"]
