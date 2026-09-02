FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json frontend/tsconfig.json frontend/vite.config.ts frontend/index.html ./
COPY frontend/src ./src
RUN npm ci && npm run build

FROM golang:1.26.6 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/periscope ./cmd/periscope

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/periscope /periscope
COPY --from=frontend /src/frontend/dist /web
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/periscope"]
