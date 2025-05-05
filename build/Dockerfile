FROM golang:1.24-alpine AS builder

WORKDIR /go/src
COPY . .

RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o app .

FROM gcr.io/distroless/static
COPY --from=builder /go/src/app .
CMD ["/app"]
