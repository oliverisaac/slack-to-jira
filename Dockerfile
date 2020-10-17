ARG APP_NAME=slack-to-jira
############################
# STEP 1 build executable binary
############################
FROM golang:alpine AS builder
# Install git.
# Git is required for fetching the dependencies.
RUN apk update && apk add --no-cache git ca-certificates 
WORKDIR $GOPATH/src/mypackage/myapp/
# Fetch dependencies.
COPY go.mod go.sum ./
# Using go get.
RUN go mod download
COPY . .
# Build the binary.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /go/bin/app

# Update certs
RUN update-ca-certificates

############################
# STEP 2 build a small image
############################
FROM scratch
WORKDIR /
# Copy our static executable.
ENV APP_PATH=/bin/${APP_NAME}
COPY --from=builder /go/bin/app /bin/${APP_NAME}
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
EXPOSE 8080
# Run the  binary
ENTRYPOINT [ "/bin/slack-to-jira" ]
