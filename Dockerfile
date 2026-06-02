ARG GO_VERSION=1.26.3

FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/augur ./cmd/augur

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/augur /app/augur

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/augur"]
