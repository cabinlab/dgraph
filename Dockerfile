# Used only by the (currently disabled) nightly build workflow

###################### Stage I ######################
FROM golang:1.25.7 AS builder
RUN apt-get update && apt-get install -y --no-install-recommends \
    bzip2 \
    git \
    && rm -rf /var/lib/apt/lists/*
ARG TARGETARCH=amd64
ARG TARGETOS=linux
WORKDIR /go/src/repo
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} make

###################### Stage II ######################
FROM ubuntu:24.04
LABEL maintainer="Dgraph <dgraph-admin@istaridigital.com>"
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    htop \
    iputils-ping \
    jq \
    less \
    sysstat \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /go/src/repo/dgraph/dgraph /usr/local/bin/
COPY --from=builder /go/src/repo/contrib/standalone/run.sh /
RUN chmod +x /run.sh
WORKDIR /dgraph
ENV GODEBUG=madvdontneed=1
CMD ["/run.sh"]
