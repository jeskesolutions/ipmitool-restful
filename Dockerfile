FROM golang:1.26-trixie AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ipmitool-microservice .

FROM debian:trixie-slim

RUN apt-get update \
    && apt-get install --yes --no-install-recommends ipmitool ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 ipmitool-api \
    && useradd --uid 10001 --gid ipmitool-api --no-create-home \
        --home-dir /var/lib/ipmitool-microservice --shell /usr/sbin/nologin ipmitool-api \
    && install -d -o ipmitool-api -g ipmitool-api -m 0750 /var/lib/ipmitool-microservice \
    && install -d -o root -g ipmitool-api -m 0750 /etc/ipmitool-microservice

COPY --from=build /out/ipmitool-microservice /usr/local/bin/ipmitool-microservice

ENV IPMI_API_SERVER_ADDRESS=0.0.0.0:8080 \
    GIN_MODE=release
WORKDIR /var/lib/ipmitool-microservice
USER 10001:10001
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/ipmitool-microservice"]
CMD ["serve", "--config", "/etc/ipmitool-microservice/config.yaml"]
