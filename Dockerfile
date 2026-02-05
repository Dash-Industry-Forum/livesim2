# Two-stage Docker-file to build a small livesim2 image
# The test content content is copied to /vod and will be used
# To enable HTTPS with fixed certificate + key,
# add files and speficy options to read them

# Build as "docker build -t livesim2 ."
# Run as "docker run -p 8888:8888 livesim2"
# Mount a volume with VoD Content at /vod to have livesim2 serve everything below it

# Build Stage
FROM golang:1.24-alpine3.21 AS BuildStage
RUN apk add git
WORKDIR /work
COPY . .
RUN go mod download
ARG COMMIT_DATE
RUN COMMIT_DATE=$(git log -1 --format=%ct)
ARG VERSION
RUN VERSION=$(git describe --tags HEAD)
RUN go build  -ldflags "-X github.com/Dash-Industry-Forum/livesim2/internal.commitVersion=$VERSION -X github.com/Dash-Industry-Forum/livesim2/internal.commitDate=$COMMIT_DATE" -o ./out/livesim2 ./cmd/livesim2/main.go
# Deploy Stage
FROM alpine:latest
WORKDIR /
COPY --from=BuildStage /work/out/ /
COPY --from=BuildStage /work/cmd/livesim2/app/testdata/assets /vod
EXPOSE 8888
ENTRYPOINT ["/livesim2", "--logformat", "json"]