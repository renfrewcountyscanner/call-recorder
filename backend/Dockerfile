FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/call-recorder ./cmd/server
FROM alpine:3.21
RUN addgroup -S recorder && adduser -S recorder -G recorder
COPY --from=build /out/call-recorder /usr/local/bin/call-recorder
USER recorder
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/call-recorder"]
