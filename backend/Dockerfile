FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/call-recorder ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/call-recorder-admin ./cmd/admin
FROM alpine:3.21
RUN addgroup -S -g 10001 recorder && adduser -S -u 10001 -G recorder recorder \
 && install -d -m 0750 -o recorder -g recorder /var/lib/call-recorder/audio
COPY --from=build /out/call-recorder /usr/local/bin/call-recorder
COPY --from=build /out/call-recorder-admin /usr/local/bin/call-recorder-admin
USER recorder
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/call-recorder"]
