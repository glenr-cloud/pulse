# beacon api — a static binary and nothing else.
FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
# Static: the runtime stage has no libc to link against at load time, and a
# binary that needs one fails at exec with a message about a missing loader.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/api .

FROM alpine:3.22

# ☠️ NON-ROOT ON PURPOSE — see the web image's Dockerfile for the reasoning.
RUN adduser -D -u 65532 beacon
COPY --from=build /out/api /usr/local/bin/api

USER 65532
EXPOSE 3000
ENTRYPOINT ["/usr/local/bin/api"]
