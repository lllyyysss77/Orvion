# syntax=docker/dockerfile:1.6

############################
# 1) 构建 WebUI（Vite）
############################
FROM node:20-alpine AS webui-builder

WORKDIR /src/webui

COPY webui/package.json webui/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci

COPY webui/ ./
RUN npm run build

############################
# 2) 构建 Go 后端（包含 embed 的 webui/dist）
############################
FROM golang:1.25-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates
ENV GOPROXY=https://proxy.golang.org,direct

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .
COPY --from=webui-builder /src/webui/dist ./webui/dist

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X 'github.com/racio/orvion/consts.Version=${VERSION}'" \
    -o ./orvion .

############################
# 3) 运行镜像
############################
FROM alpine:3.22.0

RUN apk add --no-cache \
    bash \
    bind-tools \
    ca-certificates \
    coreutils \
    curl \
    diffutils \
    file \
    findutils \
    gawk \
    git \
    grep \
    gzip \
    iproute2 \
    jq \
    make \
    netcat-openbsd \
    nodejs \
    npm \
    openssh-client \
    openssl \
    patch \
    procps \
    py3-pip \
    python3 \
    ripgrep \
    rsync \
    sed \
    sqlite \
    tar \
    tree \
    tzdata \
    unzip \
    util-linux \
    wget \
    xz \
    zip

RUN mkdir -p /orvion

COPY --from=builder /app/orvion /orvion/orvion
COPY .env.example /orvion/.env.example

WORKDIR /orvion

EXPOSE 7070

ENV TZ=Asia/Shanghai
RUN cp /usr/share/zoneinfo/${TZ} /etc/localtime && echo "${TZ}" > /etc/timezone

CMD ["./orvion"]
