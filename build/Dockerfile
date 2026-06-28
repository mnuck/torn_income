 Build stage - Use latest .* Alpine imageFROM golang:1.26.4-alpine AS builder
 Build stage - Use latest .* Alpine image
 Build stage - Use latest .* Alpine imageWORKDIR /go/src
 Build stage - Use latest .* Alpine imageCOPY . .
 Build stage - Use latest .* Alpine image
 Build stage - Use latest .* Alpine imageRUN go mod download
 Build stage - Use latest .* Alpine imageRUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o app .
 Build stage - Use latest .* Alpine image
 Build stage - Use latest .* Alpine imageFROM gcr.io/distroless/static
 Build stage - Use latest .* Alpine imageCOPY --from=builder /go/src/app .
 Build stage - Use latest .* Alpine imageCMD ["/app"]
