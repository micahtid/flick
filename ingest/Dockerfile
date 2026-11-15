# Stage 1 compiles the Go program. The Go tools stay in this stage and are
# left out of the final image.
FROM golang:1.27-alpine AS build

# Compose builds this from the project folder, not ingest/, so the shared
# pkg/chunk package can be copied in too. ingest/go.mod finds the package at
# ../pkg/chunk, so the same folder layout is kept here.
WORKDIR /src/ingest

# Download dependencies before copying the code. Docker reuses saved results
# for steps whose inputs haven't changed, so this only reruns when a go.mod or
# go.sum changes.
COPY pkg/chunk/go.mod pkg/chunk/go.sum /src/pkg/chunk/
COPY ingest/go.mod ingest/go.sum ./
RUN go mod download

# Build a single program file at /out/app. CGO_ENABLED=0 makes it fully
# self-contained, so it runs on the bare Alpine image below.
COPY pkg/chunk/ /src/pkg/chunk/
COPY ingest/ .
RUN CGO_ENABLED=0 go build -o /out/app .

# Stage 2 is the image that runs. It's a small Linux system plus the program.
FROM alpine:3.22

# Install FFmpeg, the tool that converts video. --no-cache skips keeping the
# package list, which keeps the image smaller.
RUN apk add --no-cache ffmpeg

# Copy in the program from stage 1 and run it when the container starts.
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
