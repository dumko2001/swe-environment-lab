FROM golang:1.26-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514
ENV DEBIAN_FRONTEND=noninteractive GOTOOLCHAIN=local GOMAXPROCS=4 GOFLAGS="-mod=readonly -p=4 -tags=slicelabels" GOCACHE=/opt/go-build
RUN apt-get update && apt-get install -y --no-install-recommends git patch curl ca-certificates python3 memcached && rm -rf /var/lib/apt/lists/*
COPY source/ /opt/input/
RUN cd /opt/input && sha256sum -c SOURCE.SHA256SUMS && mkdir -p /app/thanos && tar -xzf thanos-source.tar.gz -C /app/thanos --strip-components=1 && rm -rf /opt/input
COPY playground/ /opt/task-env/
RUN mkdir -p /opt/task-env/bin /var/lib/task-env && curl -fL --retry 3 https://github.com/prometheus/prometheus/releases/download/v3.5.0/prometheus-3.5.0.linux-amd64.tar.gz -o /tmp/prometheus.tar.gz && echo "e811827af26d822afb09a4f28314f61b618b12cff5369835a67f674d8b46f39a  /tmp/prometheus.tar.gz" | sha256sum -c - && tar -xzf /tmp/prometheus.tar.gz -C /opt/task-env/bin --strip-components=1 prometheus-3.5.0.linux-amd64/prometheus && rm /tmp/prometheus.tar.gz
WORKDIR /app/thanos
RUN go mod download && go build -o /opt/task-env/bin/thanos ./cmd/thanos && go build -o /opt/task-env/bin/seed /opt/task-env/seed.go && go test -run '^$' ./pkg/queryfrontend ./internal/cortex/querier/queryrange
RUN useradd -m -u 1000 -s /bin/bash agent && chown -R agent:agent /app/thanos /opt/go-build /opt/task-env /var/lib/task-env
RUN chmod +x /opt/task-env/task-env && ln -s /opt/task-env/task-env /usr/local/bin/task-env
RUN ln -s /usr/local/go/bin/go /usr/local/bin/go && ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt
USER root
CMD ["sh", "-c", "task-env start && exec sleep infinity"]
