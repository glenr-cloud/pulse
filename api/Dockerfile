FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /api .

FROM scratch
COPY --from=build /api /api
USER 65534:65534
EXPOSE 3000
ENTRYPOINT ["/api"]
