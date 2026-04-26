# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/zymo ./cmd/zymo

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/zymo /usr/local/bin/zymo
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zymo"]
CMD ["serve"]
