FROM golang:1.22.12-alpine AS build
WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -buildid=" -o /out/pulse-collector ./cmd/pulse-collector

FROM alpine:3.21.3
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 -S pulse \
    && adduser -S -D -H -u 10001 -G pulse pulse
COPY --from=build --chown=10001:10001 /out/pulse-collector /usr/local/bin/pulse-collector
USER 10001:10001
EXPOSE 6000/tcp 9093/tcp
ENTRYPOINT ["/usr/local/bin/pulse-collector"]
