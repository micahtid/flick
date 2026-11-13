# Stage 1 compiles the Go program. The Go tools stay in this stage and are
# left out of the final image.
FROM golang:1.27-alpine AS build
WORKDIR /src

# Download dependencies before copying the code. Docker reuses saved results
# for steps whose inputs haven't changed, so this only reruns when go.mod or
# go.sum change. The * lets the build work before go.sum exists.
COPY go.mod go.sum* ./
RUN go mod download

# Build a single program file at /out/app. CGO_ENABLED=0 makes it fully
# self-contained, so it runs on the bare Alpine image below.
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app .

# Stage 2 is the image that runs. It's a small Linux system plus the program.
FROM alpine:3.22
# Copy in the program from stage 1 and run it when the container starts.
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
