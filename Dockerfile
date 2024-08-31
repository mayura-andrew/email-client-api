# Stage 1: Builder Stage
FROM golang:1.22 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the Go modules manifests
COPY go.mod go.sum ./

# Download Go modules
RUN go mod download

# Copy the source code
COPY . .

# Build the Go application with CGO disabled and list files for debugging
RUN CGO_ENABLED=0 GOOS=linux go build -o app ./cmd/api && ls -l /app

# Stage 2: Final Image
FROM alpine:latest

# Set the working directory inside the container
WORKDIR /root/

# Copy the binary from the builder stage
COPY --from=builder /app/app .

# Ensure the binary is executable
RUN chmod +x ./app

# Copy the .env file
COPY .env .

# List files in the final image for debugging
RUN ls -l /root

# Copy the migration files
COPY ./migrations ./migrations

# Copy the email template file
COPY ./internal/mailer/email_template.tmpl ./internal/mailer/email_template.tmpl

# Install necessary tools
RUN apk add --no-cache bash

# Expose the application port
EXPOSE 4000

# Set environment variables
ENV DB_DSN=postgres://username:password@db:5432/emailclient?sslmode=disable \
    SMTPPORT= \
    SMTPSENDER= \
    SMTPHOST= \
    SMTPUSERNAME= \
    SMTPPASS= \
    URL=

# Command to run the application
CMD ["./app"]