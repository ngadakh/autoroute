# ---- build ----
FROM golang:1.27 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
# cmd/autoroute has no cgo dependency (the ONNX embedder is a separate binary),
# so this links a fully static executable that runs on scratch.
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/autoroute ./cmd/autoroute

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/autoroute /usr/local/bin/autoroute
COPY configs/catalogue.yaml /etc/autoroute/catalogue.yaml

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/autoroute"]
CMD ["-addr", ":8080", "-catalogue", "/etc/autoroute/catalogue.yaml", "-log-json"]
