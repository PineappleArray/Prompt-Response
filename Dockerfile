FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /router .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /router /router
COPY config.yaml /config.yaml
EXPOSE 8080
ENTRYPOINT ["/router"]
