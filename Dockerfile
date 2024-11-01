# Two-stage Docker-file to build a small livesim2 image
# The test content content is copied to /vod and will be used
# To enable HTTPS with fixed certificate + key,
# add files and speficy options to read them

# Build as "docker build -t livesim2 ."
# Run as "docker run -p 8888:8888 livesim2"

# Build Stage
FROM golang:1.23.1-alpine3.20 AS BuildStage
WORKDIR /work
COPY . .
RUN go mod download
RUN go build  -ldflags "-X github.com/Dash-Industry-Forum/livesim2/internal.commitVersion=$$(git describe --tags HEAD) -X github.com/Dash-Industry-Forum/livesim2/internal.commitDate=$$(git log -1 --format=%ct)" -o ./out/livesim2 ./cmd/livesim2/main.go
# Deploy Stage
FROM alpine:latest
WORKDIR /
COPY --from=BuildStage /work/out/ /
COPY --from=BuildStage /work/cmd/livesim2/app/testdata/assets /vod
EXPOSE 8888
ENTRYPOINT ["/livesim2", "--logformat", "json"]