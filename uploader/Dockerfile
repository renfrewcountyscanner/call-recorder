FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/call-recorder-uploader .
FROM alpine:3.21
COPY --from=build /out/call-recorder-uploader /usr/local/bin/call-recorder-uploader
ENTRYPOINT ["/usr/local/bin/call-recorder-uploader"]
