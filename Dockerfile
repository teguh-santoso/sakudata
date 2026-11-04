FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sakudata .

FROM alpine:3.20
RUN adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/sakudata /usr/local/bin/sakudata
RUN mkdir -p /data && chown app:app /data
USER app
EXPOSE 8090
VOLUME ["/data"]
ENTRYPOINT ["sakudata", "-addr", "0.0.0.0:8090", "-data", "/data"]
