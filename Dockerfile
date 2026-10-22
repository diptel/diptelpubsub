# Build stage
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/pushhub ./cmd/server

# Run stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/pushhub /app/pushhub
EXPOSE 8080
ENTRYPOINT ["/app/pushhub"]
