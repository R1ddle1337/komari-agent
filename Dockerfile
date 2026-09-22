# 原生构建阶段只生成容器标记，跨架构镜像无需执行目标架构命令。
FROM --platform=$BUILDPLATFORM alpine:3.21@sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507 AS marker
RUN touch /komari-agent-container

FROM alpine:3.21@sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507

WORKDIR /app

# Docker buildx 会在构建时自动填充这些变量
ARG TARGETOS
ARG TARGETARCH

COPY --chmod=755 komari-agent-${TARGETOS}-${TARGETARCH} /app/komari-agent

COPY --from=marker /komari-agent-container /.komari-agent-container

ENTRYPOINT ["/app/komari-agent"]
# 运行时请指定参数
# Please specify parameters at runtime.
# eg: docker run komari-agent -e example.com -t token
CMD ["--help"]
