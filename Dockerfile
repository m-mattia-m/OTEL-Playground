# Build the binary statically, so the runtime image needs no libc at all.
FROM golang:1.27 AS build

WORKDIR /src

# The dependencies change far less often than the code, so they get their own
# layer and stay cached between builds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is off, which is what makes the binary run on a distroless image.
# -trimpath keeps the build path out of the binary, so the same source always
# produces the same output.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/app

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

# The migrations and the assets are compiled into the binary, the base
# configuration is not: it is read from the working directory at startup.
COPY --from=build /out/app /app/app
COPY config.default.yaml /app/config.default.yaml

# The version is not in the base configuration, it is whatever this image was
# built from; see .env.default. APP_VERSION overwrites 'app.version'.
ARG APP_VERSION=0.0.0-dev
ENV APP_VERSION=${APP_VERSION}

# 8083 is the public API, 8084 the management API with the probes.
EXPOSE 8083 8084

USER nonroot:nonroot
ENTRYPOINT ["/app/app"]
