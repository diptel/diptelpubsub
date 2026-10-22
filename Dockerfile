# Build stage
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY . .
# go.sum is generated here (not committed) since dependencies were added without
# network access to the Go module proxy during development; this build host has
# normal internet access, so `go mod tidy` resolves and verifies them at build time.
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -o /out/pushhub ./cmd/server

# Run stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/pushhub /app/pushhub
EXPOSE 8080
ENTRYPOINT ["/app/pushhub"]
