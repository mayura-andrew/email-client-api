FROM golang:1.22 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /email-api ./cmd/api

FROM alpine:3.20

WORKDIR /app

RUN adduser -D -H appuser
COPY --from=builder /email-api ./email-api
COPY migrations ./migrations
COPY assests ./assests

# The application loads .env at startup; Compose supplies the actual values.
RUN touch .env && chown -R appuser:appuser /app

USER appuser
EXPOSE 4000

CMD ["./email-api"]
