FROM golang:1.26-alpine AS build
WORKDIR /src
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ENV COMMIT=${COMMIT}
ENV BUILD_TIME=${BUILD_TIME}
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN echo "Building commit: $COMMIT at $BUILD_TIME" \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" -o /out/call-recorder ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" -o /out/call-recorder-admin ./cmd/admin
FROM alpine:3.21
RUN apk add --no-cache tzdata \
 && addgroup -S -g 10001 recorder && adduser -S -u 10001 -G recorder recorder \
 && install -d -m 0750 -o recorder -g recorder /var/lib/call-recorder/audio
ENV TZ=America/Toronto
COPY --from=build /out/call-recorder /usr/local/bin/call-recorder
COPY --from=build /out/call-recorder-admin /usr/local/bin/call-recorder-admin
USER recorder
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/call-recorder"]
