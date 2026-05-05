FROM golang:1.22-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/cluster-meter ./cmd/cluster-meter

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/cluster-meter /cluster-meter

EXPOSE 8080

ENTRYPOINT ["/cluster-meter", "serve", "--port", "8080"]
