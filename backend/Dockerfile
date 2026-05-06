FROM golang:1.26-alpine

WORKDIR /app

# Copy go.mod first to leverage Docker cache
COPY go.mod ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application
RUN go build -o /backend-api ./cmd/api

# Expose the port the app runs on
EXPOSE 8080

# Command to run the executable
CMD ["/backend-api"]
