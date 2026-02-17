# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -ldflags '-s -w' -o /server ./cmd/server

# Run stage
FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /server
RUN chmod +x /server
ENV PORT=8080
EXPOSE 8080
CMD ["/server"]
